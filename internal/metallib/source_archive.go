package metallib

import (
	"archive/tar"
	"bytes"
	"compress/bzip2"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const maxSourceArchiveSize = 64 << 20

// ErrNoSourceArchive reports that a metallib has no supported embedded source
// archive.
var ErrNoSourceArchive = errors.New("metallib: no embedded source archive")

// SourceFile is one regular file retained in a metallib source archive.
type SourceFile struct {
	Name string
	Data []byte
}

// EmbeddedSources returns the regular files from the bzip2-compressed tar
// archive named by the metallib HSRD record.
func (m *File) EmbeddedSources() ([]SourceFile, error) {
	if m == nil {
		return nil, ErrNoSourceArchive
	}
	rangeData, err := sourceArchiveRange(m.Data)
	if err != nil {
		return nil, err
	}
	start := bytes.Index(rangeData, []byte("BZh"))
	if start < 0 {
		return nil, fmt.Errorf("%w: HSRD range has no bzip2 stream", ErrNoSourceArchive)
	}
	return readSourceArchive(bzip2.NewReader(bytes.NewReader(rangeData[start:])))
}

func sourceArchiveRange(data []byte) ([]byte, error) {
	const recordSize = 4 + 2 + 16
	for at := 0; ; {
		i := bytes.Index(data[at:], []byte("HSRD"))
		if i < 0 {
			return nil, ErrNoSourceArchive
		}
		i += at
		if len(data)-i >= recordSize && uint16(data[i+4])|uint16(data[i+5])<<8 == 16 {
			off := littleUint64(data[i+6 : i+14])
			size := littleUint64(data[i+14 : i+22])
			if off <= uint64(len(data)) && size <= uint64(len(data))-off {
				return data[off : off+size], nil
			}
		}
		at = i + 4
	}
}

func littleUint64(p []byte) uint64 {
	return uint64(p[0]) |
		uint64(p[1])<<8 |
		uint64(p[2])<<16 |
		uint64(p[3])<<24 |
		uint64(p[4])<<32 |
		uint64(p[5])<<40 |
		uint64(p[6])<<48 |
		uint64(p[7])<<56
}

func readSourceArchive(r io.Reader) ([]SourceFile, error) {
	limited := &io.LimitedReader{R: r, N: maxSourceArchiveSize + 1}
	tr := tar.NewReader(limited)
	var files []SourceFile
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("metallib: read source archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}
		if !validSourceName(h.Name) {
			return nil, fmt.Errorf("metallib: invalid source name %q", h.Name)
		}
		if h.Size < 0 || h.Size > maxSourceArchiveSize || h.Size > limited.N {
			return nil, errors.New("metallib: source archive exceeds size limit")
		}
		data, err := io.ReadAll(io.LimitReader(tr, h.Size+1))
		if err != nil {
			return nil, fmt.Errorf("metallib: read source %q: %w", h.Name, err)
		}
		if int64(len(data)) != h.Size {
			return nil, fmt.Errorf("metallib: source %q has wrong size", h.Name)
		}
		files = append(files, SourceFile{Name: h.Name, Data: data})
	}
	if limited.N < 0 {
		return nil, errors.New("metallib: source archive exceeds size limit")
	}
	if len(files) == 0 {
		return nil, errors.New("metallib: empty source archive")
	}
	return files, nil
}

func validSourceName(name string) bool {
	return name != "" && len(name) <= 4096 && utf8.ValidString(name) && !strings.ContainsRune(name, 0)
}
