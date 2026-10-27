package cmd

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/trace"
)

// funcTextureContents is the device-resources record that snapshots one region of a
// texture: C texture, @6ul region (origin x y z, size w h d), ul mipmap level,
// ul slice, U data name, <b>, ul bytesPerRow, ul bytesPerImage, ul data length.
const funcTextureContents = -10236

type texturesOptions struct {
	pngDir string
}

var texturesCmd = newTexturesCommand(&texturesOptions{})

func newTexturesCommand(opts *texturesOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "textures <trace.gputrace>",
		Short: "List texture content snapshots and export them as PNG",
		Long: `Lists the texture contents a capture recorded, one row per mip level and
slice, with the pixel format from each snapshot's data header. --png writes the
8-bit formats (R8, A8, RGBA8, BGRA8) as PNG; others are listed only.

Examples:
  gputrace textures trace.gputrace
  gputrace textures trace.gputrace --png out/`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTextures(args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.pngDir, "png", "", "Write 8-bit snapshots as PNG files in this directory")
	return cmd
}

func init() {
	rootCmd.AddCommand(texturesCmd)
}

type textureSnapshot struct {
	texture      uint64
	w, h, d      uint64
	level, slice uint64
	name         string
	unused       bool // recorded in unused-device-resources: not used by the captured frame
}

func readTextureSnapshots(dir string) ([]textureSnapshot, error) {
	var snaps []textureSnapshot
	files, _ := filepath.Glob(filepath.Join(dir, "*device-resources-*"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		recs, err := trace.WalkFramedRecords(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", filepath.Base(f), err)
		}
		unused := strings.HasPrefix(filepath.Base(f), "unused-")
		for _, r := range recs {
			if r.FuncID != funcTextureContents {
				continue
			}
			a, _, err := r.Args()
			if err != nil || len(a) < 13 {
				continue
			}
			snaps = append(snaps, textureSnapshot{
				texture: a[0].Uint,
				w:       a[4].Uint, h: a[5].Uint, d: a[6].Uint,
				level: a[7].Uint, slice: a[8].Uint,
				name:   a[9].Str,
				unused: unused,
			})
		}
	}
	return snaps, nil
}

// readTextureHeader reads only the data header when the snapshot is a bundle file;
// a store0 blob is inflated whole.
func readTextureHeader(dir string, idx *trace.StoreIndex, name string) (trace.TextureDataHeader, error) {
	var b []byte
	if e, ok := idx.Lookup(name); ok && e.Kind == trace.StoreKindFile {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return trace.TextureDataHeader{}, err
		}
		defer f.Close()
		b = make([]byte, 0x100)
		if _, err := io.ReadFull(f, b); err != nil {
			return trace.TextureDataHeader{}, err
		}
	} else {
		var err error
		if b, err = trace.ReadStoreBlob(dir, idx, name); err != nil {
			return trace.TextureDataHeader{}, err
		}
	}
	h, _, err := trace.ParseTextureData(b)
	return h, err
}

func runTextures(dir string, opts *texturesOptions) error {
	snaps, err := readTextureSnapshots(dir)
	if err != nil {
		return err
	}
	idx, err := trace.ReadStoreIndex(filepath.Join(dir, "index"))
	if err != nil {
		return err
	}
	if opts.pngDir != "" {
		if err := os.MkdirAll(opts.pngDir, 0o755); err != nil {
			return err
		}
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TEXTURE\tSIZE\tLEVEL\tSLICE\tFORMAT\tFRAME\tDATA")
	exported := 0
	for _, s := range snaps {
		format := "?"
		h, err := readTextureHeader(dir, idx, s.name)
		if err == nil {
			format = trace.PixelFormatName(h.PixelFormat)
		}
		frame := "used"
		if s.unused {
			frame = "unused"
		}
		fmt.Fprintf(w, "%#x\t%dx%dx%d\t%d\t%d\t%s\t%s\t%s\n", s.texture, s.w, s.h, s.d, s.level, s.slice, format, frame, s.name)
		if opts.pngDir == "" || err != nil || s.d != 1 || bytesPerPixel(h.PixelFormat) == 0 {
			continue
		}
		if err := writeSnapshotPNG(dir, idx, opts.pngDir, s.name); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", s.name, err)
			continue
		}
		exported++
	}
	w.Flush()
	if opts.pngDir != "" {
		fmt.Printf("\n%d of %d snapshots written to %s\n", exported, len(snaps), opts.pngDir)
	}
	return nil
}

func bytesPerPixel(format uint64) int {
	switch format {
	case 1, 10:
		return 1
	case 70, 71, 80, 81:
		return 4
	}
	return 0
}

func writeSnapshotPNG(dir string, idx *trace.StoreIndex, outDir, name string) error {
	b, err := trace.ReadStoreBlob(dir, idx, name)
	if err != nil {
		return err
	}
	h, px, err := trace.ParseTextureData(b)
	if err != nil {
		return err
	}
	bpp := uint64(bytesPerPixel(h.PixelFormat))
	if h.Width == 0 || h.Height == 0 || h.BytesPerRow < h.Width*bpp || uint64(len(px)) < h.BytesPerRow*(h.Height-1)+h.Width*bpp {
		return fmt.Errorf("%d pixel bytes do not hold %dx%d at %d bytes per row", len(px), h.Width, h.Height, h.BytesPerRow)
	}
	var img image.Image
	if bpp == 1 {
		g := image.NewGray(image.Rect(0, 0, int(h.Width), int(h.Height)))
		for y := uint64(0); y < h.Height; y++ {
			copy(g.Pix[int(y)*g.Stride:], px[y*h.BytesPerRow:y*h.BytesPerRow+h.Width])
		}
		img = g
	} else {
		c := image.NewNRGBA(image.Rect(0, 0, int(h.Width), int(h.Height)))
		bgra := h.PixelFormat == 80 || h.PixelFormat == 81
		for y := uint64(0); y < h.Height; y++ {
			row := px[y*h.BytesPerRow:]
			for x := uint64(0); x < h.Width; x++ {
				p, q := row[4*x:4*x+4], c.Pix[int(y)*c.Stride+4*int(x):]
				if bgra {
					q[0], q[1], q[2], q[3] = p[2], p[1], p[0], p[3]
				} else {
					copy(q[:4], p)
				}
			}
		}
		img = c
	}
	f, err := os.Create(filepath.Join(outDir, name+".png"))
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
