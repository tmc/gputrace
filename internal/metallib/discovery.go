package metallib

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FoundFile represents a discovered MTLB file.
type FoundFile struct {
	Name string
	Path string
	Size int64
}

// FindFiles scans the trace directory for files with MTLB magic.
func FindFiles(tracePath string) ([]FoundFile, error) {
	var results []FoundFile

	// Walk the trace directory
	err := filepath.Walk(tracePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Check file size (header is at least 4 bytes)
		if info.Size() < 4 {
			return nil
		}

		// Read the first 4 bytes to check for magic
		f, err := os.Open(path)
		if err != nil {
			// Ignore errors opening individual files (e.g. permissions)
			return nil
		}
		defer f.Close()

		var magic [4]byte
		if _, err := io.ReadFull(f, magic[:]); err != nil {
			return nil
		}

		if string(magic[:]) == "MTLB" {
			results = append(results, FoundFile{
				Name: info.Name(),
				Path: path,
				Size: info.Size(),
			})
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking trace directory: %w", err)
	}

	return results, nil
}
