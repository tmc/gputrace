package trace

import (
	"encoding/binary"
	"fmt"
)

const textureDataMagic = "erutpac\x00"

// TextureDataHeader starts every texture snapshot (MTLTexture-*, IOSurface-*,
// CAMetalLayer-*), whether a bundle file or a store0 blob:
//
//	0x00 "erutpac\0", u16 2, u16 1, u32 header size (0x100)
//	0x10 u64 1, u64 MTLPixelFormat, u64 width, u64 height, u64 depth,
//	     u64 bytesPerRow, u64 bytesPerImage
type TextureDataHeader struct {
	HeaderSize    int
	PixelFormat   uint64
	Width         uint64
	Height        uint64
	Depth         uint64
	BytesPerRow   uint64
	BytesPerImage uint64
}

// ParseTextureData splits a texture snapshot into its header and pixel bytes.
func ParseTextureData(b []byte) (TextureDataHeader, []byte, error) {
	var h TextureDataHeader
	if len(b) < 0x48 || string(b[:8]) != textureDataMagic {
		return h, nil, fmt.Errorf("no texture data header")
	}
	h.HeaderSize = int(binary.LittleEndian.Uint32(b[12:]))
	if h.HeaderSize < 0x48 || h.HeaderSize > len(b) {
		return h, nil, fmt.Errorf("bad texture header size %d", h.HeaderSize)
	}
	f := func(off int) uint64 { return binary.LittleEndian.Uint64(b[off:]) }
	h.PixelFormat, h.Width, h.Height, h.Depth = f(0x18), f(0x20), f(0x28), f(0x30)
	h.BytesPerRow, h.BytesPerImage = f(0x38), f(0x40)
	return h, b[h.HeaderSize:], nil
}

// PixelFormatName names the MTLPixelFormat values seen in captures so far.
func PixelFormatName(f uint64) string {
	if n, ok := pixelFormatNames[f]; ok {
		return n
	}
	return fmt.Sprintf("format(%d)", f)
}

var pixelFormatNames = map[uint64]string{
	1: "A8Unorm", 10: "R8Unorm", 30: "RG8Unorm", 25: "R16Float", 55: "R32Float",
	65: "RG16Float", 70: "RGBA8Unorm", 71: "RGBA8Unorm_sRGB", 80: "BGRA8Unorm",
	81: "BGRA8Unorm_sRGB", 90: "RGB10A2Unorm", 92: "RG11B10Float", 105: "RG32Float",
	115: "RGBA16Float", 125: "RGBA32Float", 130: "BC1_RGBA", 131: "BC1_RGBA_sRGB",
	132: "BC2_RGBA", 133: "BC2_RGBA_sRGB", 134: "BC3_RGBA", 135: "BC3_RGBA_sRGB",
	140: "BC4_RUnorm", 142: "BC5_RGUnorm", 150: "BC6H_RGBFloat", 152: "BC7_RGBAUnorm",
	153: "BC7_RGBAUnorm_sRGB", 252: "Depth32Float", 253: "Stencil8",
	260: "Depth32Float_Stencil8",
}
