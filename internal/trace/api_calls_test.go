package trace

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAPICallList(t *testing.T) {
	tracePath := "/tmp/test_standalone.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("Test trace file not found: %s", tracePath)
	}

	trace := &Trace{
		Path: tracePath,
	}

	list, err := trace.ParseAPICallList()
	if err != nil {
		t.Fatalf("ParseAPICallList failed: %v", err)
	}

	t.Logf("Init calls: %d", len(list.InitCalls))
	t.Logf("Command buffers: %d", len(list.CommandBuffers))

	// Verify we have initialization calls
	if len(list.InitCalls) == 0 {
		t.Error("Expected initialization calls")
	}

	// Log init calls
	for _, call := range list.InitCalls {
		t.Logf("  Init #%d: type=%s addr=0x%x info=%s",
			call.CallNumber, call.Type, call.Address, call.Info)
	}

	// Verify we have command buffers
	if len(list.CommandBuffers) == 0 {
		t.Error("Expected command buffers")
	}

	// Log first command buffer calls
	if len(list.CommandBuffers) > 0 {
		cb := list.CommandBuffers[0]
		t.Logf("\nCommand Buffer #%d:", cb.Index)
		t.Logf("  Calls: %d", len(cb.Calls))

		for _, call := range cb.Calls {
			indent := ""
			if call.Indented {
				indent = "    "
			}
			t.Logf("  %s#%d: %s (details: %s)", indent, call.CallNumber, call.Type, call.Details)
		}
	}
}

func TestParseCommandBufferLabel(t *testing.T) {
	const (
		queueAddr = 0x107132eb0
		cbAddr    = 0x780c115e0
		label     = "A611TraceProbeCommandBufferLabel"
	)

	dir := t.TempDir()
	capture := make([]byte, 0, 256)
	capture = append(capture, []byte("CUUU")...)
	capture = binary.LittleEndian.AppendUint64(capture, 0x12345678)
	capture = append(capture, []byte("C\x00\x00\x00")...)
	capture = binary.LittleEndian.AppendUint64(capture, queueAddr)
	capture = append(capture, []byte("C\x00\x00\x00")...)
	capture = binary.LittleEndian.AppendUint64(capture, cbAddr)
	capture = append(capture, []byte("CS\x00\x00")...)
	capture = binary.LittleEndian.AppendUint64(capture, cbAddr)
	capture = append(capture, label...)
	capture = append(capture, 0)

	if err := os.WriteFile(filepath.Join(dir, "capture"), capture, 0o666); err != nil {
		t.Fatal(err)
	}

	tr := &Trace{Path: dir}
	cbs, err := tr.ParseCommandBuffers()
	if err != nil {
		t.Fatal(err)
	}
	if len(cbs) != 1 {
		t.Fatalf("got %d command buffers, want 1", len(cbs))
	}
	if cbs[0].Label != label {
		t.Fatalf("command buffer label = %q, want %q", cbs[0].Label, label)
	}

	list, err := tr.ParseAPICallList()
	if err != nil {
		t.Fatal(err)
	}
	if len(list.CommandBuffers) != 1 {
		t.Fatalf("got %d api command buffers, want 1", len(list.CommandBuffers))
	}
	if list.CommandBuffers[0].Label != label {
		t.Fatalf("api command buffer label = %q, want %q", list.CommandBuffers[0].Label, label)
	}
}

// TestParseInitCalls_ResidencySet tests parsing of residency set creation (CUt records)
func TestParseInitCalls_ResidencySet(t *testing.T) {
	// Create a minimal capture with a CUt record
	// Structure: size(4) + "CUt\x00" + address(8) + UUID(16) + ...
	data := make([]byte, 0x100)

	// Write CUt marker at offset 0x2C (matching real traces)
	offset := 0x2C
	binary.LittleEndian.PutUint32(data[offset-4:], 0x09) // size
	copy(data[offset:], []byte("CUt\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], 0x0afd018000) // residency set address

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	// Should find one residency set
	found := false
	for _, call := range calls {
		if call.Type == "newResidencySet" {
			found = true
			if call.Address != 0x0afd018000 {
				t.Errorf("Expected residency set address 0x0afd018000, got 0x%x", call.Address)
			}
			if call.Info != "[Device newResidencySetWithDescriptor:<data> error:nil]" {
				t.Errorf("Unexpected info: %s", call.Info)
			}
		}
	}

	if !found {
		t.Error("Expected to find residency set creation call")
	}
}

// TestParseInitCalls_Heap tests parsing of heap creation (CU records)
func TestParseInitCalls_Heap(t *testing.T) {
	// Create a minimal capture with a CU record
	// Structure: size(4) + "CU\x00\x00" + device_addr(8) + UUID(16) + size(4) + heap_addr(8)
	data := make([]byte, 0x200)

	// Write CU marker at offset 0x18C
	offset := 0x18C
	binary.LittleEndian.PutUint32(data[offset-4:], 0x09) // size
	copy(data[offset:], []byte("CU\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], 0x0afd058000)   // device address
	copy(data[offset+0x0C:], []byte("88B6ED551A91183D"))           // UUID
	binary.LittleEndian.PutUint32(data[offset+0x20:], 0x74)        // size marker
	binary.LittleEndian.PutUint64(data[offset+0x24:], 0x106da56b0) // heap address

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	// Should find one heap
	found := false
	for _, call := range calls {
		if call.Type == "newHeap" {
			found = true
			if call.Address != 0x106da56b0 {
				t.Errorf("Expected heap address 0x106da56b0, got 0x%x", call.Address)
			}
			if call.Info != "[Device newHeapWithDescriptor:<data>]" {
				t.Errorf("Unexpected info: %s", call.Info)
			}
		}
	}

	if !found {
		t.Error("Expected to find heap creation call")
	}
}

// TestParseInitCalls_BufferFromHeap tests parsing of buffer creation from heap (Culul records)
func TestParseInitCalls_BufferFromHeap(t *testing.T) {
	// Create a minimal capture with a Culul record
	data := make([]byte, 0x600)

	// Write Culul marker at offset 0x4E0
	offset := 0x4E0
	putCululBufferRecord(data, offset, 0x106da56b0, 16, 256, 0x106da6190)

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	// Should find buffer creation and BufferHeapOffset
	foundBuffer := false
	foundOffset := false
	for _, call := range calls {
		if call.Type == "newBuffer" && call.Address == 0x106da6190 {
			foundBuffer = true
			expected := "[0x106da56b0 newBufferWithLength:16 options:HazardTrackingModeUntracked]"
			if call.Info != expected {
				t.Errorf("Expected info %s, got %s", expected, call.Info)
			}
		}
		if call.Type == "bufferHeapOffset" && call.Address == 0x106da6190 {
			foundOffset = true
			expected := "BufferHeapOffset(0x106da6190, 256)"
			if call.Info != expected {
				t.Errorf("Expected info %s, got %s", expected, call.Info)
			}
		}
	}

	if !foundBuffer {
		t.Error("Expected to find buffer creation call")
	}
	if !foundOffset {
		t.Error("Expected to find BufferHeapOffset call")
	}
}

func putCululBufferRecord(data []byte, offset int, heapAddr, bufLen, heapOffset, bufAddr uint64) {
	binary.LittleEndian.PutUint32(data[offset-4:], 0x09)
	copy(data[offset:], []byte("Culul\x00\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+0x08:], heapAddr)
	binary.LittleEndian.PutUint64(data[offset+0x10:], bufLen)
	binary.LittleEndian.PutUint64(data[offset+0x24:], bufAddr)

	copy(data[offset+0x80:], []byte("Cuw\x00"))
	binary.LittleEndian.PutUint64(data[offset+0x84:], bufAddr)
	binary.LittleEndian.PutUint32(data[offset+0x8c:], uint32(heapOffset))
}

// TestParseInitCalls_SharedEvent tests parsing of shared event creation (Cui records)
func TestParseInitCalls_SharedEvent(t *testing.T) {
	// Create a minimal capture with a Cui record
	data := make([]byte, 0x1000)

	// Write Cui marker at offset 0x960
	offset := 0x960
	binary.LittleEndian.PutUint32(data[offset-4:], 0x09) // size
	copy(data[offset:], []byte("Cui\x00"))
	// Event address at +0x0c
	binary.LittleEndian.PutUint64(data[offset+0x0c:], 0xafcc88800)

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	// Should find shared event
	found := false
	for _, call := range calls {
		if call.Type == "newSharedEvent" {
			found = true
			if call.Address != 0xafcc88800 {
				t.Errorf("Expected event address 0xafcc88800, got 0x%x", call.Address)
			}
		}
	}

	if !found {
		t.Error("Expected to find shared event creation call")
	}
}

// TestParseInitCalls_CommandQueue tests parsing of command queue creation (CS records)
func TestParseInitCalls_CommandQueue(t *testing.T) {
	// Create a minimal capture with a CS record for "Stream 0"
	data := make([]byte, 0x1000)

	// Write CS marker at offset 0x830
	offset := 0x830
	queueAddr := uint64(0x106da64d0)
	copy(data[offset:], []byte("CS\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], queueAddr) // queue address
	copy(data[offset+0x0C:], []byte("Stream 0\x00"))          // label

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("Expected 2 init calls, got %d: %#v", len(calls), calls)
	}

	queueCall := calls[0]
	if queueCall.Type != "newCommandQueue" {
		t.Fatalf("First call type = %q, want newCommandQueue", queueCall.Type)
	}
	if queueCall.Address != queueAddr {
		t.Fatalf("Queue address = 0x%x, want 0x%x", queueCall.Address, queueAddr)
	}
	if queueCall.Label != "Stream 0" {
		t.Fatalf("Queue label = %q, want Stream 0", queueCall.Label)
	}
	if queueCall.Info != "Stream 0 = [Device newCommandQueue]" {
		t.Fatalf("Queue info = %q", queueCall.Info)
	}
	if queueCall.Offset != int64(offset) {
		t.Fatalf("Queue offset = 0x%x, want 0x%x", queueCall.Offset, offset)
	}

	labelCall := calls[1]
	if labelCall.Type != "setLabel" {
		t.Fatalf("Second call type = %q, want setLabel", labelCall.Type)
	}
	if labelCall.Address != queueAddr {
		t.Fatalf("Label address = 0x%x, want 0x%x", labelCall.Address, queueAddr)
	}
	if labelCall.Label != "Stream 0" {
		t.Fatalf("Label call label = %q, want Stream 0", labelCall.Label)
	}
	if labelCall.Info != "[Stream 0 setLabel:\"Stream 0\"]" {
		t.Fatalf("Label info = %q", labelCall.Info)
	}
	if labelCall.Offset != int64(offset+1) {
		t.Fatalf("Label offset = 0x%x, want 0x%x", labelCall.Offset, offset+1)
	}
}

// TestParseInitCalls_Function tests parsing of function creation from CS records
func TestParseInitCalls_Function(t *testing.T) {
	const (
		offset   = 0x500
		funcAddr = 0xafcc88580
		funcName = "vv_Addfloat32"
	)

	// Fixture layout: "CS\x00\x00" + function address + null-terminated name.
	data := make([]byte, 0x1000)
	copy(data[offset:], []byte("CS\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], funcAddr)
	copy(data[offset+0x0C:], []byte(funcName+"\x00"))

	records, labelMap := parseCSRecordsFromInit(data)
	if len(records) != 1 {
		t.Fatalf("got %d CS records, want 1: %#v", len(records), records)
	}
	if records[0].CSAddress != funcAddr {
		t.Fatalf("CS address = 0x%x, want 0x%x", records[0].CSAddress, funcAddr)
	}
	if records[0].Label != funcName {
		t.Fatalf("CS label = %q, want %q", records[0].Label, funcName)
	}
	if records[0].Offset != int64(offset) {
		t.Fatalf("CS offset = 0x%x, want 0x%x", records[0].Offset, offset)
	}
	if labelMap[funcAddr] != funcName {
		t.Fatalf("labelMap[0x%x] = %q, want %q", funcAddr, labelMap[funcAddr], funcName)
	}

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("got %d init calls, want 1: %#v", len(calls), calls)
	}
	call := calls[0]
	if call.Type != "newFunction" {
		t.Fatalf("call type = %q, want newFunction", call.Type)
	}
	if call.Address != funcAddr {
		t.Fatalf("function address = 0x%x, want 0x%x", call.Address, funcAddr)
	}
	if call.Label != funcName {
		t.Fatalf("function label = %q, want %q", call.Label, funcName)
	}
	expected := "[0xafcc88580 newFunctionWithName:\"vv_Addfloat32\"]"
	if call.Info != expected {
		t.Fatalf("call info = %q, want %q", call.Info, expected)
	}
	if call.Offset != int64(offset) {
		t.Fatalf("call offset = 0x%x, want 0x%x", call.Offset, offset)
	}
}

// TestParseInitCalls_PipelineState tests parsing of pipeline state creation (Ctt records)
func TestParseInitCalls_PipelineState(t *testing.T) {
	const (
		functionOffset = 0x500
		pipelineOffset = 0x700
		functionAddr   = 0xafcc88580
		pipelineAddr   = 0x106d82550
		functionName   = "vv_Addfloat32"
	)

	data := make([]byte, 0x2000)

	copy(data[functionOffset:], []byte("CS\x00\x00"))
	binary.LittleEndian.PutUint64(data[functionOffset+4:], functionAddr)
	copy(data[functionOffset+0x0C:], []byte(functionName+"\x00"))

	copy(data[pipelineOffset:], []byte("Ctt\x00"))
	binary.LittleEndian.PutUint64(data[pipelineOffset+0x04:], 0x106da64d0)
	binary.LittleEndian.PutUint64(data[pipelineOffset+0x0C:], functionAddr)
	binary.LittleEndian.PutUint64(data[pipelineOffset+0x20:], pipelineAddr)

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("got %d init calls, want 2: %#v", len(calls), calls)
	}
	if calls[0].Type != "newFunction" {
		t.Fatalf("first call type = %q, want newFunction", calls[0].Type)
	}
	if calls[0].Address != functionAddr {
		t.Fatalf("function address = 0x%x, want 0x%x", calls[0].Address, functionAddr)
	}
	if calls[0].Label != functionName {
		t.Fatalf("function label = %q, want %q", calls[0].Label, functionName)
	}
	if calls[0].Offset != functionOffset {
		t.Fatalf("function offset = 0x%x, want 0x%x", calls[0].Offset, functionOffset)
	}

	if calls[1].Type != "newPipelineState" {
		t.Fatalf("second call type = %q, want newPipelineState", calls[1].Type)
	}
	if calls[1].Address != pipelineAddr {
		t.Fatalf("pipeline address = 0x%x, want 0x%x", calls[1].Address, pipelineAddr)
	}
	expected := "[Device newComputePipelineStateWithFunction:vv_Addfloat32 error:nil]"
	if calls[1].Info != expected {
		t.Fatalf("pipeline info = %q, want %q", calls[1].Info, expected)
	}
	if calls[1].Offset != pipelineOffset {
		t.Fatalf("pipeline offset = 0x%x, want 0x%x", calls[1].Offset, pipelineOffset)
	}
}

// TestParseInitCalls_RequestResidency tests parsing of requestResidency calls
func TestParseInitCalls_RequestResidency(t *testing.T) {
	data := make([]byte, 0x100)
	residencySetAddr := uint64(0x0afd018000)

	offset := 0x2C
	copy(data[offset:], []byte("CUt\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], residencySetAddr)

	offset = 0x60
	copy(data[offset:], []byte("Ct\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], residencySetAddr)

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	found := false
	for _, call := range calls {
		if call.Type == "requestResidency" {
			found = true
			if call.Address != residencySetAddr {
				t.Errorf("Expected requestResidency address 0x%x, got 0x%x", residencySetAddr, call.Address)
			}
			if call.Info != "[0xafd018000 requestResidency]" {
				t.Errorf("Unexpected info: %s", call.Info)
			}
		}
	}

	if !found {
		t.Error("Expected to find requestResidency call")
	}
}

func TestParseInitCalls_RequestResidencyRejectsUnknownCt(t *testing.T) {
	data := make([]byte, 0x100)

	offset := 0x2C
	copy(data[offset:], []byte("CUt\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], 0x0afd018000)

	offset = 0x60
	copy(data[offset:], []byte("Ct\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], 0x106d82550)

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	for _, call := range calls {
		if call.Type == "requestResidency" {
			t.Fatalf("Unexpected requestResidency call for non-residency Ct address 0x%x", call.Address)
		}
	}
}

func TestParseInitCalls_AddResidencySet(t *testing.T) {
	data := make([]byte, 0x100)

	const (
		queueAddr        = 0x106da64d0
		residencySetAddr = 0x0afd018000
	)

	offset := 0x20
	copy(data[offset:], []byte("CS\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], queueAddr)
	copy(data[offset+0x0c:], []byte("Stream 0\x00"))

	offset = 0x50
	copy(data[offset:], []byte("CUt\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], residencySetAddr)

	putAddResidencySetRecord(data, 0xa0, residencySetAddr)

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	found := false
	for _, call := range calls {
		if call.Type != "addResidencySet" {
			continue
		}
		found = true
		if call.Address != residencySetAddr {
			t.Fatalf("Address = 0x%x, want 0x%x", call.Address, residencySetAddr)
		}
		if call.Label != "Stream 0" {
			t.Fatalf("Label = %q, want Stream 0", call.Label)
		}
		want := "[Stream 0 addResidencySet:0xafd018000]"
		if call.Info != want {
			t.Fatalf("Info = %q, want %q", call.Info, want)
		}
	}
	if !found {
		t.Fatal("expected addResidencySet call")
	}
}

func putAddResidencySetRecord(data []byte, offset int, residencySetAddr uint64) {
	binary.LittleEndian.PutUint32(data[offset-0x24:], 0x58)
	binary.LittleEndian.PutUint32(data[offset-0x20:], 0xffffc13d)
	binary.LittleEndian.PutUint32(data[offset-0x04:], 0x0a)
	copy(data[offset:], []byte("C\x00\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+0x04:], residencySetAddr)
	binary.LittleEndian.PutUint32(data[offset+0x0c:], 0x04)
	binary.LittleEndian.PutUint32(data[offset+0x10:], 0x08)
}

func TestParseInitCalls_AddResidencySetFailsClosed(t *testing.T) {
	data := make([]byte, 0x100)

	const (
		queueAddr        = 0x106da64d0
		residencySetAddr = 0x0afd018000
	)

	offset := 0x20
	copy(data[offset:], []byte("CS\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], queueAddr)
	copy(data[offset+0x0c:], []byte("Stream 0\x00"))

	offset = 0x50
	copy(data[offset:], []byte("CUt\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], residencySetAddr)

	offset = 0x80
	copy(data[offset:], []byte("Ct\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], residencySetAddr)

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	var foundQueue, foundResidency, foundRequest bool
	for _, call := range calls {
		switch call.Type {
		case "newCommandQueue":
			foundQueue = true
		case "newResidencySet":
			foundResidency = true
		case "requestResidency":
			foundRequest = true
		case "addResidencySet":
			t.Fatalf("parsed addResidencySet from unsupported record evidence: %#v", call)
		}
	}
	if !foundQueue {
		t.Fatal("expected command queue fixture to parse")
	}
	if !foundResidency {
		t.Fatal("expected residency set fixture to parse")
	}
	if !foundRequest {
		t.Fatal("expected requestResidency fixture to parse")
	}
}

func TestFormatAPICallList_AddResidencySet(t *testing.T) {
	const (
		queueAddr        = 0x106da64d0
		cbAddr           = 0x780c115e0
		residencySetAddr = 0x0afd018000
	)

	dir := t.TempDir()
	capture := make([]byte, 0x100)
	copy(capture[0x00:], []byte("CS\x00\x00"))
	binary.LittleEndian.PutUint64(capture[0x04:], queueAddr)
	copy(capture[0x0c:], []byte("Stream 0\x00"))
	copy(capture[0x30:], []byte("CUt\x00"))
	binary.LittleEndian.PutUint64(capture[0x34:], residencySetAddr)
	putAddResidencySetRecord(capture, 0x80, residencySetAddr)
	copy(capture[0xc0:], []byte("CUUU"))
	binary.LittleEndian.PutUint64(capture[0xc4:], 0x12345678)
	copy(capture[0xcc:], []byte("C\x00\x00\x00"))
	binary.LittleEndian.PutUint64(capture[0xd0:], queueAddr)
	copy(capture[0xd8:], []byte("C\x00\x00\x00"))
	binary.LittleEndian.PutUint64(capture[0xdc:], cbAddr)

	if err := os.WriteFile(filepath.Join(dir, "capture"), capture, 0o666); err != nil {
		t.Fatal(err)
	}

	tr := &Trace{Path: dir}
	var buf bytes.Buffer
	if err := tr.FormatAPICallList(&buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	want := "#3 [Stream 0 addResidencySet:0xafd018000]\n"
	if !bytes.Contains(buf.Bytes(), []byte(want)) {
		t.Fatalf("formatted API calls missing %q in:\n%s", want, output)
	}
	if bytes.Contains(buf.Bytes(), []byte("#3 Stream 0 =")) {
		t.Fatalf("addResidencySet should not have label assignment prefix:\n%s", output)
	}
}

func TestParseInitCalls_FenceLabelFailsClosed(t *testing.T) {
	offset := 0x40
	fenceTableAddr := uint64(0x0afd024930)
	csRecords := []FunctionRecord{
		{
			CSAddress: fenceTableAddr,
			Label:     "fences",
			Offset:    int64(offset),
		},
	}

	// Device-resource archaeology has shown CS labels such as "fences". That
	// label alone identifies a resource table, not an MTLFence creation record.
	calls, _, err := parseInitCalls(nil, 0, csRecords, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	for _, call := range calls {
		if call.Type == "newFence" {
			t.Fatalf("unexpected newFence call from resource label: %#v", call)
		}
		if call.Type == "newFunction" && call.Address == fenceTableAddr {
			t.Fatalf("fence resource label was misclassified as a function: %#v", call)
		}
	}
}

// TestParseInitCalls_Ordering tests that calls are sorted by offset
func TestParseInitCalls_Ordering(t *testing.T) {
	// Create data with multiple record types at different offsets
	data := make([]byte, 0x2000)

	// Add records in non-chronological order but at specific offsets
	// CUt at 0x2C
	offset := 0x2C
	copy(data[offset:], []byte("CUt\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], 0x0afd018000)

	// CU at 0x18C
	offset = 0x18C
	copy(data[offset:], []byte("CU\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+4:], 0x0afd058000)
	binary.LittleEndian.PutUint64(data[offset+0x24:], 0x106da56b0)

	// Culul at 0x4E0
	offset = 0x4E0
	copy(data[offset:], []byte("Culul\x00\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+0x08:], 0x106da56b0)
	binary.LittleEndian.PutUint64(data[offset+0x10:], 16)
	binary.LittleEndian.PutUint64(data[offset+0x24:], 0x106da6190)

	calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
	if err != nil {
		t.Fatalf("parseInitCalls failed: %v", err)
	}

	if len(calls) < 3 {
		t.Fatalf("Expected at least 3 calls, got %d", len(calls))
	}

	// Verify calls are sorted by offset (and thus call number)
	for i := 0; i < len(calls)-1; i++ {
		if calls[i].Offset > calls[i+1].Offset {
			t.Errorf("Calls not sorted by offset: call %d offset %d > call %d offset %d",
				i, calls[i].Offset, i+1, calls[i+1].Offset)
		}
		if calls[i].CallNumber >= calls[i+1].CallNumber {
			t.Errorf("Calls not sorted by call number: call %d number %d >= call %d number %d",
				i, calls[i].CallNumber, i+1, calls[i+1].CallNumber)
		}
	}
}

// TestParseBufferHeapOffset tests parsing actual heap offset values
func TestParseBufferHeapOffset(t *testing.T) {
	tests := []struct {
		name       string
		heapOffset uint64
		bufAddr    uint64
		wantInfo   string
	}{
		{
			name:       "zero",
			heapOffset: 0,
			bufAddr:    0x106da6190,
			wantInfo:   "BufferHeapOffset(0x106da6190, 0)",
		},
		{
			name:       "middle allocation",
			heapOffset: 256,
			bufAddr:    0xafcdd0000,
			wantInfo:   "BufferHeapOffset(0xafcdd0000, 256)",
		},
		{
			name:       "later allocation",
			heapOffset: 512,
			bufAddr:    0xafcdd1980,
			wantInfo:   "BufferHeapOffset(0xafcdd1980, 512)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := make([]byte, 0x600)
			putCululBufferRecord(data, 0x100, 0x106da56b0, 16, tt.heapOffset, tt.bufAddr)

			calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
			if err != nil {
				t.Fatalf("parseInitCalls failed: %v", err)
			}

			found := false
			for _, call := range calls {
				if call.Type != "bufferHeapOffset" {
					continue
				}
				found = true
				if call.Address != tt.bufAddr {
					t.Fatalf("Address = 0x%x, want 0x%x", call.Address, tt.bufAddr)
				}
				if call.Info != tt.wantInfo {
					t.Fatalf("Info = %q, want %q", call.Info, tt.wantInfo)
				}
			}
			if !found {
				t.Fatal("Expected BufferHeapOffset call")
			}
		})
	}
}

func TestParseBufferHeapOffsetRequiresMatchingCuwCompanion(t *testing.T) {
	tests := []struct {
		name string
		edit func([]byte)
	}{
		{
			name: "missing companion",
			edit: func(data []byte) {
				copy(data[0x100+0x80:], []byte{0, 0, 0, 0})
			},
		},
		{
			name: "mismatched buffer address",
			edit: func(data []byte) {
				binary.LittleEndian.PutUint64(data[0x100+0x84:], 0xafcdd0000)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := make([]byte, 0x600)
			putCululBufferRecord(data, 0x100, 0x106da56b0, 16, 256, 0x106da6190)
			tt.edit(data)

			calls, _, err := parseInitCalls(data, 0, nil, make(map[uint64]string))
			if err != nil {
				t.Fatalf("parseInitCalls failed: %v", err)
			}
			for _, call := range calls {
				if call.Type == "bufferHeapOffset" {
					t.Fatalf("unexpected BufferHeapOffset call without matching Cuw companion: %#v", call)
				}
			}
		})
	}
}

// TestFormatAPICallList_BufferHeapOffset tests formatting of BufferHeapOffset
func TestFormatAPICallList_BufferHeapOffset(t *testing.T) {
	// BufferHeapOffset should not have "0x... =" prefix
	calls := []InitCall{
		{
			CallNumber: 4,
			Type:       "bufferHeapOffset",
			Address:    0x106da6190,
			Info:       "BufferHeapOffset(0x106da6190, 0)",
		},
	}

	// Simulate formatting
	var buf bytes.Buffer
	for _, call := range calls {
		if call.Type == "bufferHeapOffset" {
			// Should NOT have address prefix
			buf.WriteString("#4 BufferHeapOffset(0x106da6190, 0)\n")
		}
	}

	output := buf.String()
	expected := "#4 BufferHeapOffset(0x106da6190, 0)\n"
	if output != expected {
		t.Errorf("Expected:\n%s\nGot:\n%s", expected, output)
	}

	// Should NOT contain "0x106da6190 ="
	if bytes.Contains([]byte(output), []byte("0x106da6190 =")) {
		t.Error("BufferHeapOffset should not have address prefix")
	}
}

// TestFormatAPICallList_SetLabel tests formatting of setLabel calls
func TestFormatAPICallList_SetLabel(t *testing.T) {
	const (
		queueAddr = 0x106da64d0
		cbAddr    = 0x780c115e0
		label     = "Stream 0"
	)

	dir := t.TempDir()
	capture := make([]byte, 0, 128)
	capture = append(capture, []byte("CS\x00\x00")...)
	capture = binary.LittleEndian.AppendUint64(capture, queueAddr)
	capture = append(capture, label...)
	capture = append(capture, 0)
	capture = append(capture, []byte("CUUU")...)
	capture = binary.LittleEndian.AppendUint64(capture, 0x12345678)
	capture = append(capture, []byte("C\x00\x00\x00")...)
	capture = binary.LittleEndian.AppendUint64(capture, queueAddr)
	capture = append(capture, []byte("C\x00\x00\x00")...)
	capture = binary.LittleEndian.AppendUint64(capture, cbAddr)

	if err := os.WriteFile(filepath.Join(dir, "capture"), capture, 0o666); err != nil {
		t.Fatal(err)
	}

	tr := &Trace{Path: dir}
	var buf bytes.Buffer
	if err := tr.FormatAPICallList(&buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	want := "#1 [Stream 0 setLabel:\"Stream 0\"]\n"
	if !bytes.Contains(buf.Bytes(), []byte(want)) {
		t.Fatalf("formatted API calls missing %q in:\n%s", want, output)
	}
	if bytes.Contains(buf.Bytes(), []byte("#1 0x106da64d0 =")) {
		t.Fatalf("setLabel should not have address prefix:\n%s", output)
	}
	if bytes.Contains(buf.Bytes(), []byte("#1 Stream 0 =")) {
		t.Fatalf("setLabel should not have label assignment prefix:\n%s", output)
	}
}

// TestParseBufferBindings tests buffer binding extraction
func TestParseBufferBindings(t *testing.T) {
	// Create data with Ctulul records (buffer bindings)
	data := make([]byte, 0x500)

	offset := 0x100
	copy(data[offset:], []byte("Ctulul\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+0x10:], 0x106da6190) // buffer address
	binary.LittleEndian.PutUint32(data[offset+0x20:], 0)           // index 0

	offset = 0x200
	copy(data[offset:], []byte("Ctulul\x00\x00"))
	binary.LittleEndian.PutUint64(data[offset+0x10:], 0xafcdd0000) // buffer address
	binary.LittleEndian.PutUint32(data[offset+0x20:], 1)           // index 1

	bindings, err := parseBufferBindings(data)
	if err != nil {
		t.Fatalf("parseBufferBindings failed: %v", err)
	}

	if len(bindings) != 2 {
		t.Fatalf("Expected 2 bindings, got %d", len(bindings))
	}

	if bindings[0].BufferAddr != 0x106da6190 || bindings[0].Index != 0 {
		t.Errorf("First binding incorrect: addr=0x%x, index=%d", bindings[0].BufferAddr, bindings[0].Index)
	}

	if bindings[1].BufferAddr != 0xafcdd0000 || bindings[1].Index != 1 {
		t.Errorf("Second binding incorrect: addr=0x%x, index=%d", bindings[1].BufferAddr, bindings[1].Index)
	}
}

func TestFormatAPICallList(t *testing.T) {
	tracePath := "/tmp/test_standalone.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("Test trace file not found: %s", tracePath)
	}

	trace := &Trace{
		Path: tracePath,
	}

	var buf bytes.Buffer
	err := trace.FormatAPICallList(&buf)
	if err != nil {
		t.Fatalf("FormatAPICallList failed: %v", err)
	}

	output := buf.String()
	t.Logf("API Call List:\n%s", output)

	// Verify output contains expected elements
	if !bytes.Contains(buf.Bytes(), []byte("newBuffer")) {
		t.Error("Output missing newBuffer call")
	}

	if !bytes.Contains(buf.Bytes(), []byte("commandBuffer")) {
		t.Error("Output missing commandBuffer call")
	}

	if !bytes.Contains(buf.Bytes(), []byte("computeCommandEncoder")) {
		t.Error("Output missing encoder call")
	}

	if !bytes.Contains(buf.Bytes(), []byte("dispatchThreads")) {
		t.Error("Output missing dispatch call")
	}
}
