package replay

import (
	"encoding/binary"
	"errors"
	"testing"

	tracepkg "github.com/tmc/gputrace/internal/trace"
)

func TestParseDeviceResources(t *testing.T) {
	const (
		functionAddr = 0x1000
		pipelineAddr = 0x2000
	)

	data := mtspData(
		csRecord(functionAddr, "vector_add"),
		cttRecord(functionAddr, pipelineAddr),
	)
	rs := NewReplayState(&Trace{
		DeviceResources: map[string][]byte{"0xabc": data},
		FunctionToName:  make(map[uint64]string),
	})

	got, err := rs.ParseDeviceResources()
	if err != nil {
		t.Fatal(err)
	}
	fn, ok := got.Functions[functionAddr]
	if !ok {
		t.Fatalf("missing function 0x%x", functionAddr)
	}
	if fn.Name != "vector_add" {
		t.Fatalf("function name = %q, want vector_add", fn.Name)
	}

	pso, ok := got.Pipelines[pipelineAddr]
	if !ok {
		t.Fatalf("missing pipeline 0x%x", pipelineAddr)
	}
	if pso.FunctionAddr != functionAddr {
		t.Fatalf("pipeline function address = 0x%x, want 0x%x", pso.FunctionAddr, functionAddr)
	}
	if pso.FunctionName != "vector_add" {
		t.Fatalf("pipeline function name = %q, want vector_add", pso.FunctionName)
	}
}

func TestDiscoverFunctionsSortsByAddress(t *testing.T) {
	rs := NewReplayState(&Trace{
		DeviceResources: map[string][]byte{
			"0xabc": mtspData(
				csRecord(0x3000, "z_kernel"),
				csRecord(0x1000, "a_kernel"),
			),
		},
		FunctionToName: make(map[uint64]string),
	})

	got, err := rs.DiscoverFunctions()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len(functions) = %d, want 2", len(got))
	}
	if got[0].Address != 0x1000 || got[1].Address != 0x3000 {
		t.Fatalf("addresses = [0x%x 0x%x], want [0x1000 0x3000]", got[0].Address, got[1].Address)
	}
	if rs.FunctionNames[0x1000] != "a_kernel" {
		t.Fatalf("FunctionNames[0x1000] = %q, want a_kernel", rs.FunctionNames[0x1000])
	}
}

func TestParseDeviceResourcesInvalidMagic(t *testing.T) {
	rs := NewReplayState(&Trace{
		DeviceResources: map[string][]byte{"0xabc": []byte("nope")},
		FunctionToName:  make(map[uint64]string),
	})

	_, err := rs.ParseDeviceResources()
	if !errors.Is(err, tracepkg.ErrInvalidMagic) {
		t.Fatalf("err = %v, want invalid magic", err)
	}
}

func mtspData(parts ...[]byte) []byte {
	data := make([]byte, 16)
	copy(data, tracepkg.MagicMTSP)
	for _, p := range parts {
		data = append(data, p...)
	}
	return data
}

func csRecord(addr uint64, name string) []byte {
	rec := make([]byte, 12+len(name)+1)
	copy(rec, []byte("CS\x00\x00"))
	binary.LittleEndian.PutUint64(rec[4:12], addr)
	copy(rec[12:], name)
	return rec
}

func cttRecord(functionAddr, pipelineAddr uint64) []byte {
	rec := make([]byte, 0x28)
	copy(rec, []byte("Ctt\x00"))
	binary.LittleEndian.PutUint64(rec[0x0c:0x14], functionAddr)
	binary.LittleEndian.PutUint64(rec[0x20:0x28], pipelineAddr)
	return rec
}
