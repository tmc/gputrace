package gputrace

import (
	"fmt"
	"os"
	"testing"
)

func TestParseObjCTrace(t *testing.T) {
	tracePath := "/tmp/objc_metal_trace.gputrace"
	
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skip("Test trace not found")
	}

	trace, err := Open(tracePath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	fmt.Println("\n=== Metadata ===")
	fmt.Printf("UUID: %s\n", trace.Metadata.UUID)
	fmt.Printf("Graphics API: %d\n", trace.Metadata.GraphicsAPI)
	
	fmt.Println("\n=== Kernel Names ===")
	for _, name := range trace.KernelNames {
		fmt.Printf("  • %s\n", name)
	}
	
	fmt.Println("\n=== Encoder Labels ===")
	for _, label := range trace.EncoderLabels {
		fmt.Printf("  • %s\n", label)
	}
	
	fmt.Println("\n=== Buffer Labels ===")
	for _, label := range trace.BufferLabels {
		fmt.Printf("  • %s\n", label)
	}
}
