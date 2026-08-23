package cuptitrace

import (
	"bufio"
	"bytes"
	"io"
)

func bufioReader(r io.Reader) io.Reader { return r }

// bufioScannerLines splits data into newline-terminated lines.
func bufioScannerLines(data []byte) [][]byte {
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			out = append(out, sc.Bytes())
		}
	}
	return out
}
