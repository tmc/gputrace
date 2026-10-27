package trace

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func framedRecord(funcID int32, flags uint32, sig string, body []byte) []byte {
	s := append([]byte(sig), 0)
	for len(s)%4 != 0 {
		s = append(s, 0)
	}
	rec := make([]byte, framedHeaderSize, framedHeaderSize+len(s)+len(body))
	binary.LittleEndian.PutUint32(rec[4:], uint32(funcID))
	binary.LittleEndian.PutUint32(rec[0x20:], flags)
	rec = append(append(rec, s...), body...)
	binary.LittleEndian.PutUint32(rec, uint32(len(rec)))
	return rec
}

func le64(v ...uint64) []byte {
	var b []byte
	for _, x := range v {
		b = binary.LittleEndian.AppendUint64(b, x)
	}
	return b
}

func TestWalkFramedRecordsAndArgs(t *testing.T) {
	snapshot := le64(0x7f00, 0, 0, 0, 128, 128, 1, 0, 0)
	snapshot = append(snapshot, "MTLTexture-1-0-mipmap0-slice0\x00\x00\x00"...)
	snapshot = append(snapshot, le64(512, 65536, 65792)...)
	ret := append([]byte{'t', 0, 0, 0}, le64(0xabc)...)
	trailer := append([]byte{3, 0, 0, 0, 8, 0, 0, 0}, le64(1, 2, 3)...)

	data := []byte("MTSP\x00\x04\x00\x00")
	data = append(data, framedRecord(-10236, 0x4, "C@6ulululU<b>ululul", snapshot)...)
	data = append(data, framedRecord(-16310, FlagReturnValue|FlagTrailer, "CU", append(append(le64(0x7f01), "AEB6B44D547DFDC4\x00\x00\x00\x00"...), append(ret, trailer...)...))...)
	data = append(data, framedRecord(-16255, 0, "Cffff", append(le64(0x7f02), 0, 0, 0x80, 0x3f, 0, 0, 0, 0x40, 0, 0, 0, 0, 0, 0, 0, 0))...)

	recs, err := WalkFramedRecords(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3", len(recs))
	}

	args, rest, err := recs[0].Args()
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 14 || args[4].Uint != 128 || args[9].Str != "MTLTexture-1-0-mipmap0-slice0" || args[11].Uint != 512 {
		t.Errorf("snapshot args = %v", args)
	}
	if len(rest) != 0 {
		t.Errorf("snapshot left %d bytes", len(rest))
	}

	args, rest, err = recs[1].Args()
	if err != nil {
		t.Fatal(err)
	}
	if args[1].Str != "AEB6B44D547DFDC4" || !bytes.Equal(rest, append(ret, trailer...)) {
		t.Errorf("CU args = %v rest = % x", args, rest)
	}
	if ts, ok := recs[1].Trailer(); !ok || ts != [3]uint64{1, 2, 3} {
		t.Errorf("trailer = %v %v", ts, ok)
	}

	args, _, err = recs[2].Args()
	if err != nil {
		t.Fatal(err)
	}
	if args[1].Float != 1 || args[2].Float != 2 || args[3].Float != 0 || args[4].Float != 0 {
		t.Errorf("floats = %v", args)
	}
}

func TestWalkFramedRecordsRejectsBadSize(t *testing.T) {
	data := append([]byte("MTSP\x00\x04\x00\x00"), framedRecord(-1, 0, "C", le64(1))...)
	binary.LittleEndian.PutUint32(data[8:], 1<<20)
	if _, err := WalkFramedRecords(data); err == nil {
		t.Fatal("want an error for a record running past the file")
	}
}

func TestSignatureTokens(t *testing.T) {
	got, err := SignatureTokens("CtU<b>ulul@6uluiS")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"C", "t", "U", "<b>", "ul", "ul", "@6ul", "ui", "S"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if _, err := SignatureTokens("Cp"); err == nil {
		t.Error("want an error for an unknown token")
	}
}

func TestReadStoreIndexAndBlob(t *testing.T) {
	dir := t.TempDir()
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write([]byte("descriptor bytes"))
	zw.Close()
	store := append([]byte("pad"), z.Bytes()...)
	if err := os.WriteFile(filepath.Join(dir, "store0"), store, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte("meta"), 0o644); err != nil {
		t.Fatal(err)
	}

	names := []string{"315F954AD9D508B0", "metadata"}
	const buckets = 4
	idx := []byte("xdic")
	idx = binary.LittleEndian.AppendUint32(idx, 0)
	idx = binary.LittleEndian.AppendUint32(idx, buckets)
	idx = binary.LittleEndian.AppendUint32(idx, uint32(len(names)))
	idx = binary.LittleEndian.AppendUint32(idx, uint32(len(names)))
	for i := range buckets {
		v := uint32(0xffffffff)
		if i < len(names) {
			v = uint32(i)
		}
		idx = binary.LittleEndian.AppendUint32(idx, v)
		idx = binary.LittleEndian.AppendUint32(idx, v)
		idx = binary.LittleEndian.AppendUint32(idx, 0xffffffff)
	}
	idx = binary.LittleEndian.AppendUint32(idx, 16)
	idx = binary.LittleEndian.AppendUint32(idx, uint32(z.Len()))
	idx = append(idx, le64(3, StoreKindBlob)...)
	idx = binary.LittleEndian.AppendUint32(idx, 4)
	idx = binary.LittleEndian.AppendUint32(idx, 1)
	idx = append(idx, le64(0, StoreKindFile)...)
	for _, n := range names {
		idx = binary.LittleEndian.AppendUint16(idx, uint16(len(n)+1))
	}
	for _, n := range names {
		idx = append(append(idx, n...), 0)
	}
	if err := os.WriteFile(filepath.Join(dir, "index"), idx, 0o644); err != nil {
		t.Fatal(err)
	}

	x, err := ReadStoreIndex(filepath.Join(dir, "index"))
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := x.Lookup("metadata"); !ok || e.Kind != StoreKindFile || e.Size != 4 {
		t.Errorf("metadata entry = %+v %v", e, ok)
	}
	b, err := ReadStoreBlob(dir, x, "315F954AD9D508B0")
	if err != nil || string(b) != "descriptor bytes" {
		t.Errorf("blob = %q, %v", b, err)
	}
	b, err = ReadStoreBlob(dir, x, "metadata")
	if err != nil || string(b) != "meta" {
		t.Errorf("file = %q, %v", b, err)
	}
	if _, err := ReadStoreBlob(dir, x, "missing"); err == nil {
		t.Error("want an error for a missing name")
	}
}

func TestParseTextureData(t *testing.T) {
	b := make([]byte, 0x100+8)
	copy(b, textureDataMagic)
	binary.LittleEndian.PutUint16(b[8:], 2)
	binary.LittleEndian.PutUint16(b[10:], 1)
	binary.LittleEndian.PutUint32(b[12:], 0x100)
	copy(b[0x10:], le64(1, 80, 2, 1, 1, 8, 8))
	copy(b[0x100:], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	h, px, err := ParseTextureData(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.PixelFormat != 80 || h.Width != 2 || h.BytesPerRow != 8 || len(px) != 8 || px[0] != 1 {
		t.Errorf("header = %+v, pixels = % x", h, px)
	}
	if PixelFormatName(80) != "BGRA8Unorm" {
		t.Errorf("format 80 = %s", PixelFormatName(80))
	}
	if _, _, err := ParseTextureData([]byte("short")); err == nil {
		t.Error("want an error without the magic")
	}
}
