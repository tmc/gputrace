package cmd

import (
	"io"
	"os"

	"github.com/tmc/gputrace/internal/gpuevent"
)

// readCapture decodes events from r and, when samplesPath is non-empty,
// joins the sample file into the same capture.
func readCapture(r io.Reader, samplesPath string) (gpuevent.Capture, error) {
	cap, err := gpuevent.DecodeJSONL(r)
	if err != nil {
		return cap, err
	}
	if samplesPath != "" {
		sf, err := os.Open(samplesPath)
		if err == nil {
			defer sf.Close()
			sc, err := gpuevent.DecodeJSONL(sf)
			if err == nil {
				cap.Samples = append(cap.Samples, sc.Samples...)
			}
		} else if !os.IsNotExist(err) {
			return cap, err
		}
	}
	return cap, nil
}
