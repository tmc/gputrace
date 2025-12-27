package main

import (
	"fmt"
	"os"

	"github.com/tmc/gputrace"
)

func main() {
	trace, err := gputrace.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	fmt.Println("Trace Kernel Names:")
	for _, k := range trace.KernelNames {
		fmt.Println(k)
	}
}
