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

// commandOutputPathIsStdout reports whether path names standard output,
// treating an empty path as stdout (the default when no -o is given).
func commandOutputPathIsStdout(path string) bool {
	return path == "" || outputPathIsExplicitStdout(path)
}

// outputPathIsExplicitStdout reports whether path explicitly names standard
// output ("-" or /dev/stdout). Unlike commandOutputPathIsStdout it does not
// treat an empty path as stdout; callers that default empty to a generated
// filename use this form.
func outputPathIsExplicitStdout(path string) bool {
	return path == "-" || path == "/dev/stdout"
}
