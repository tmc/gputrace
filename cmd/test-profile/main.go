package main

import (
	"compress/gzip"
	"fmt"
	"log"
	"os"

	"github.com/google/pprof/profile"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: test-profile <file.pprof.gz>")
	}

	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatalf("Open: %v", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		log.Fatalf("Gzip: %v", err)
	}
	defer gr.Close()

	prof, err := profile.Parse(gr)
	if err != nil {
		log.Fatalf("Parse: %v", err)
	}

	fmt.Printf("Profile loaded successfully!\n")
	fmt.Printf("Sample types: %d\n", len(prof.SampleType))
	for i, st := range prof.SampleType {
		fmt.Printf("  [%d] %s/%s\n", i, st.Type, st.Unit)
	}
	fmt.Printf("Samples: %d\n", len(prof.Sample))
	fmt.Printf("Locations: %d\n", len(prof.Location))
	fmt.Printf("Functions: %d\n", len(prof.Function))

	// Validate
	if err := prof.CheckValid(); err != nil {
		log.Fatalf("Validation failed: %v", err)
	}

	fmt.Printf("Profile is valid!\n")
}
