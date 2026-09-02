package metallib

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestListDebugRecords(t *testing.T) {
	data := append([]byte("prefixDEBI-not-a-record"), debugRecordForTest("kernels/a.metal", 17, "a.air")...)
	data = append(data, debugRecordForTest("kernels/b.metal", 29, "b.air")...)
	want := []DebugRecord{
		{Source: "kernels/a.metal", Line: 17, Dependency: "a.air"},
		{Source: "kernels/b.metal", Line: 29, Dependency: "b.air"},
	}
	if got := (&File{Data: data}).ListDebugRecords(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ListDebugRecords = %#v, want %#v", got, want)
	}
}

func TestListDebugRecordsRefusesUnterminatedSource(t *testing.T) {
	record := debugRecordForTest("kernels/a.metal", 17, "a.air")
	record[6+4+len("kernels/a.metal")] = 'x'
	if got := (&File{Data: record}).ListDebugRecords(); len(got) != 0 {
		t.Fatalf("ListDebugRecords = %#v, want none", got)
	}
}

func TestListDebugRecordsUsesDeclaredPrivateMetadata(t *testing.T) {
	decoy := debugRecordForTest("decoy.metal", 1, "decoy.air")
	wantRecord := debugRecordForTest("real.metal", 2, "real.air")
	data := append(append([]byte{}, decoy...), wantRecord...)
	lib := &File{
		Data: data,
		Header: Header{
			PrivateMetadata:     uint64(len(decoy)),
			PrivateMetadataSize: uint64(len(wantRecord)),
		},
	}
	want := []DebugRecord{{Source: "real.metal", Line: 2, Dependency: "real.air"}}
	if got := lib.ListDebugRecords(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ListDebugRecords = %#v, want %#v", got, want)
	}
}

func TestListFunctionDebug(t *testing.T) {
	data := buildTaggedMTLBForTest(
		taggedFunctionForTest("a", 0, 1, 0),
		taggedFunctionForTest("b", 1, 1, 0),
	)
	private := privateMetadataForTest(
		debugRecordForTest("a.metal", 3, "a.air"),
		debugRecordForTest("b.metal", 5, "b.air"),
	)
	off := len(data)
	data = append(data, private...)
	lib, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	lib.Header.PrivateMetadata = uint64(off)
	lib.Header.PrivateMetadataSize = uint64(len(private))
	pairs, err := lib.ListFunctionDebug()
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 || pairs[0].Function.Name != "a" || pairs[0].Debug.Source != "a.metal" || pairs[1].Function.Name != "b" || pairs[1].Debug.Source != "b.metal" {
		t.Fatalf("pairs = %#v", pairs)
	}
}

func TestListFunctionDebugRefusesCountMismatch(t *testing.T) {
	data := buildTaggedMTLBForTest(
		taggedFunctionForTest("a", 0, 1, 0),
		taggedFunctionForTest("b", 1, 1, 0),
	)
	private := privateMetadataForTest(debugRecordForTest("a.metal", 3, "a.air"))
	off := len(data)
	data = append(data, private...)
	lib, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	lib.Header.PrivateMetadata = uint64(off)
	lib.Header.PrivateMetadataSize = uint64(len(private))
	if _, err := lib.ListFunctionDebug(); err == nil {
		t.Fatal("ListFunctionDebug succeeded with a missing entry")
	}
}

func debugRecordForTest(source string, line uint32, dependency string) []byte {
	sourcePayload := make([]byte, 4, 5+len(source))
	binary.LittleEndian.PutUint32(sourcePayload, line)
	sourcePayload = append(sourcePayload, source...)
	sourcePayload = append(sourcePayload, 0)
	dependencyPayload := append([]byte(dependency), 0)
	record := taggedFieldForTest("DEBI", sourcePayload)
	record = append(record, taggedFieldForTest("DEPF", dependencyPayload)...)
	return record
}

func privateMetadataForTest(entries ...[]byte) []byte {
	var data []byte
	for _, entry := range entries {
		size := uint32(4 + len(entry) + 4)
		data = append(data, littleUint32ForTest(size)...)
		data = append(data, entry...)
		data = append(data, []byte("ENDT")...)
	}
	return data
}
