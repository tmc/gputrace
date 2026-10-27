package trace

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Store index entry kinds.
const (
	StoreKindBlob = 1 // a zlib stream in store0
	StoreKindFile = 2 // a file in the bundle
)

// StoreEntry is one name in the bundle's index file.
type StoreEntry struct {
	Name string
	Size uint32 // uncompressed size
	// CompressedSize is the zlib stream length in store0 for a blob; for a file it
	// holds the entry's own index.
	CompressedSize uint32
	Offset         uint64 // stream offset in store0 (blobs only)
	Kind           uint64
}

// StoreIndex maps the names a trace refers to, such as the 16-hex-digit keys in
// "U" arguments, to their data.
type StoreIndex struct {
	Entries []StoreEntry
	byName  map[string]int
}

// ReadStoreIndex parses an xdic index file:
//
//	0x00 "xdic", u32 0, u32 bucketCount, u32 entryCount, u32 entryCount
//	0x14 bucketCount x {u32 entry, u32 entry, u32 0xffffffff}, empty = all 0xffffffff
//	     entryCount x {u32 size, u32 compressedSize, u64 offset, u64 kind}
//	     entryCount x u16 name length including its NUL
//	     names, NUL-terminated, sorted
func ReadStoreIndex(path string) (*StoreIndex, error) {
	d, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(d) < 0x14 || string(d[:4]) != MagicXDIC {
		return nil, fmt.Errorf("%s: not an xdic index", path)
	}
	buckets := int(binary.LittleEndian.Uint32(d[8:]))
	n := int(binary.LittleEndian.Uint32(d[12:]))
	ent := 0x14 + buckets*12
	lens := ent + n*24
	pos := lens + n*2
	if pos > len(d) {
		return nil, fmt.Errorf("%s: %d entries do not fit in %d bytes", path, n, len(d))
	}
	idx := &StoreIndex{Entries: make([]StoreEntry, n), byName: make(map[string]int, n)}
	for i := range n {
		e := d[ent+i*24:]
		l := int(binary.LittleEndian.Uint16(d[lens+i*2:]))
		if l == 0 || pos+l > len(d) {
			return nil, fmt.Errorf("%s: name %d runs past the file", path, i)
		}
		name := string(bytes.TrimRight(d[pos:pos+l], "\x00"))
		pos += l
		idx.Entries[i] = StoreEntry{
			Name:           name,
			Size:           binary.LittleEndian.Uint32(e),
			CompressedSize: binary.LittleEndian.Uint32(e[4:]),
			Offset:         binary.LittleEndian.Uint64(e[8:]),
			Kind:           binary.LittleEndian.Uint64(e[16:]),
		}
		idx.byName[name] = i
	}
	return idx, nil
}

// Lookup returns the entry for a name.
func (x *StoreIndex) Lookup(name string) (StoreEntry, bool) {
	i, ok := x.byName[name]
	if !ok {
		return StoreEntry{}, false
	}
	return x.Entries[i], true
}

// ReadStoreBlob returns the data for a name in the trace bundle at dir: a blob is
// inflated from store0, a file is read from the bundle.
func ReadStoreBlob(dir string, idx *StoreIndex, name string) ([]byte, error) {
	e, ok := idx.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("%q is not in the index", name)
	}
	if e.Kind == StoreKindFile {
		return os.ReadFile(filepath.Join(dir, name))
	}
	if e.Kind != StoreKindBlob {
		return nil, fmt.Errorf("%q has unknown kind %d", name, e.Kind)
	}
	f, err := os.Open(filepath.Join(dir, "store0"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := zlib.NewReader(io.NewSectionReader(f, int64(e.Offset), int64(e.CompressedSize)))
	if err != nil {
		return nil, fmt.Errorf("%q: %w", name, err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", name, err)
	}
	if len(out) != int(e.Size) {
		return out, fmt.Errorf("%q: inflated %d bytes, index says %d", name, len(out), e.Size)
	}
	return out, nil
}
