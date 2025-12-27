package main

import (
	"fmt"
	"os"

	"github.com/tmc/gputrace"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: verify_kernels <trace_path>")
		os.Exit(1)
	}

	trace, err := gputrace.Open(os.Args[1])
	if err != nil {
		panic(err)
	}

	fmt.Printf("Total Kernel Names: %d\n", len(trace.KernelNames))
	fmt.Printf("Total Encoder Labels: %d\n", len(trace.EncoderLabels))

	for _, k := range trace.EncoderLabels {
		fmt.Printf("Encoder: %s\n", k)
	}
}
