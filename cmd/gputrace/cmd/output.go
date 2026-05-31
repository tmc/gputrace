package cmd

import (
	"io"
	"os"
)

func createCommandOutput(path string) (io.Writer, func() error, error) {
	if commandOutputPathIsStdout(path) {
		return os.Stdout, nil, nil
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}

func commandOutputPathIsStdout(path string) bool {
	return path == "" || path == "-" || path == "/dev/stdout"
}
