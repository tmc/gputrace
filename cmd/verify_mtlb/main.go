package main

import (
	"fmt"
	"os"

	"github.com/tmc/gputrace/internal/mtlb"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: verify_mtlb <mtlb_file>")
		os.Exit(1)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}

	file, err := mtlb.ParseMTLB(data)
	if err != nil {
		panic(err)
	}

	fmt.Printf("MTLB Version: %d\n", file.Header.Version)
	fmt.Printf("Total Size: %d\n", file.Header.TotalSize)

	funcs, err := file.ListFunctions()
	if err != nil {
		panic(err)
	}

	fmt.Printf("Found %d functions:\n", len(funcs))
	for i, f := range funcs {
		if i >= 20 {
			fmt.Printf("... and %d more\n", len(funcs)-20)
			break
		}
		fmt.Printf("- %s\n", f)
	}
}
