package graph

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestResourceGraphDOT(t *testing.T) {
	tr := testResourceTrace()

	var out bytes.Buffer
	if err := NewDOTGenerator().Generate(&out, tr, &Config{Type: "resources", ShowMemory: true}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		"digraph GPUTraceResources",
		"Op1",
		"Op2",
		"buf_0x2000",
		"enc0 -> res_2000",
		"enc1 -> res_2000",
		"ReadWrite",
		"2 accesses",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("DOT output missing %q:\n%s", want, got)
		}
	}
}

func TestResourceGraphMermaid(t *testing.T) {
	tr := testResourceTrace()

	var out bytes.Buffer
	if err := NewMermaidGenerator().Generate(&out, tr, &Config{Type: "resources", ShowMemory: true}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		"graph LR",
		"enc0[\"Op1\"]",
		"enc1[\"Op2\"]",
		"res_2000",
		"enc0 -->|ReadWrite| res_2000",
		"enc1 -->|ReadWrite| res_2000",
		"2 accesses",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Mermaid output missing %q:\n%s", want, got)
		}
	}
}

func TestResourceGraphOrdersUseEventsByTraceOffset(t *testing.T) {
	tr := testResourceTraceWithUseBetweenEncoders()

	var out bytes.Buffer
	if err := NewDOTGenerator().Generate(&out, tr, &Config{Type: "resources"}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := out.String()

	want := `enc0 -> res_3000 [label="Read"];`
	if !strings.Contains(got, want) {
		t.Fatalf("DOT output missing %q:\n%s", want, got)
	}
	bad := `enc1 -> res_3000 [label="Read"];`
	if strings.Contains(got, bad) {
		t.Fatalf("DOT output assigned use event to later encoder:\n%s", got)
	}
}

func testResourceTrace() *trace.Trace {
	buf := make([]byte, 256)
	offset := 16

	offset = putDispatch(buf, offset, 0xAAAA1111, 0x2000)
	offset += 32
	offset = putDispatch(buf, offset, 0xBBBB2222, 0x2000)

	return &trace.Trace{
		CaptureData: buf[:offset],
		DeviceLabels: map[uint64]string{
			0xAAAA1111: "Op1",
			0xBBBB2222: "Op2",
		},
		FunctionToName: map[uint64]string{
			0xAAAA1111: "Op1",
			0xBBBB2222: "Op2",
		},
	}
}

func testResourceTraceWithUseBetweenEncoders() *trace.Trace {
	buf := make([]byte, 512)
	offset := 16

	offset = putDispatch(buf, offset, 0xAAAA1111, 0x2000)
	offset = putUse(buf, offset, 0x3000)
	offset = putDispatch(buf, offset, 0xBBBB2222, 0x4000)

	return &trace.Trace{
		CaptureData: buf[:offset],
		DeviceLabels: map[uint64]string{
			0xAAAA1111: "Op1",
			0xBBBB2222: "Op2",
		},
		FunctionToName: map[uint64]string{
			0xAAAA1111: "Op1",
			0xBBBB2222: "Op2",
		},
	}
}

func putDispatch(buf []byte, offset int, function, resource uint64) int {
	copy(buf[offset:], []byte{0x43, 0x74, 0x00, 0x00})
	offset += 4
	binary.LittleEndian.PutUint64(buf[offset:], 0x1000)
	offset += 8
	binary.LittleEndian.PutUint64(buf[offset:], function)
	offset += 8
	binary.LittleEndian.PutUint32(buf[offset:], 1)
	offset += 4
	binary.LittleEndian.PutUint32(buf[offset:], 8)
	offset += 4
	binary.LittleEndian.PutUint64(buf[offset:], resource)
	return offset + 8
}

func putUse(buf []byte, offset int, resource uint64) int {
	copy(buf[offset:], []byte("Ctulul\x00"))
	binary.LittleEndian.PutUint32(buf[offset+32:], 1)
	binary.LittleEndian.PutUint32(buf[offset+36:], 8)
	binary.LittleEndian.PutUint64(buf[offset+40:], resource)
	return offset + 48
}
