package command

import (
	"encoding/binary"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestParseAPICallsInRegionParsesCtRecord(t *testing.T) {
	data := make([]byte, 0x50)
	binary.LittleEndian.PutUint32(data[0x00:0x04], uint32(len(data)))
	binary.LittleEndian.PutUint32(data[0x04:0x08], 0xffffc01c)
	binary.LittleEndian.PutUint32(data[0x20:0x24], 8)
	copy(data[0x24:0x28], []byte("Ct\x00\x00"))
	binary.LittleEndian.PutUint64(data[0x28:0x30], 0xbd7895c00)
	binary.LittleEndian.PutUint64(data[0x30:0x38], 0x16bbfe278)
	binary.LittleEndian.PutUint32(data[0x38:0x3c], 2)
	binary.LittleEndian.PutUint32(data[0x3c:0x40], 8)
	binary.LittleEndian.PutUint64(data[0x40:0x48], 0x134a46714)
	binary.LittleEndian.PutUint64(data[0x48:0x50], 0x133d3bd58)

	calls, err := parseAPICallsInRegion(data, 0x1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}

	call := calls[0]
	if call.RecordType != trace.RecordTypeCt {
		t.Fatalf("RecordType = %q, want %q", call.RecordType, trace.RecordTypeCt)
	}
	if call.CommandFlags != 0xffffc01c {
		t.Fatalf("CommandFlags = 0x%x, want 0xffffc01c", call.CommandFlags)
	}
	if call.PipelineAddr != 0xbd7895c00 {
		t.Fatalf("PipelineAddr = 0x%x, want 0xbd7895c00", call.PipelineAddr)
	}
	if call.FunctionAddr != 0x16bbfe278 {
		t.Fatalf("FunctionAddr = 0x%x, want 0x16bbfe278", call.FunctionAddr)
	}
	if call.BindingCount != 2 {
		t.Fatalf("BindingCount = %d, want 2", call.BindingCount)
	}
	if call.Type != 2 {
		t.Fatalf("Type = %d, want compatibility binding count 2", call.Type)
	}
	if got, want := call.Offset, int64(0x1000); got != want {
		t.Fatalf("Offset = 0x%x, want 0x%x", got, want)
	}
	if len(call.BufferBindings) != 2 || call.BufferBindings[0] != 0x134a46714 || call.BufferBindings[1] != 0x133d3bd58 {
		t.Fatalf("BufferBindings = %#x", call.BufferBindings)
	}
}
