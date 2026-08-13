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
