package cmd

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestRenderEncoderTreeSetLabelDoesNotNest(t *testing.T) {
	records := []trace.MTSPRecord{
		csRecord(0x13, "first"),
		csRecord(0x13, "second"),
		cRecord(0x3b),
		csRecord(0x13, "third"),
	}
	var out bytes.Buffer
	if err := renderEncoderTree(&out, new(trace.Trace), records, nil, &treeOptions{limit: -1}); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n")[1:] {
		if strings.HasPrefix(line, "  ") {
			t.Fatalf("setLabel changed tree depth:\n%s", out.String())
		}
	}
}

func csRecord(flags uint32, label string) trace.MTSPRecord {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[4:], flags)
	return trace.MTSPRecord{Type: trace.RecordTypeCS, Data: data, Label: label}
}

func cRecord(flags uint32) trace.MTSPRecord {
	data := make([]byte, 24)
	binary.LittleEndian.PutUint32(data[4:], flags)
	copy(data[8:], "C\x00\x00\x00")
	return trace.MTSPRecord{Type: trace.RecordTypeC, Data: data}
}
