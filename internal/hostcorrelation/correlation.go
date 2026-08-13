// Package hostcorrelation validates evidence used to correlate host events
// with GPU traces.
package hostcorrelation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

const (
	Schema       = "gputrace.host-correlation/v1"
	maximumBytes = 1 << 20
)

var (
	ErrInvalid      = errors.New("invalid host correlation")
	ErrUncorrelated = errors.New("host and GPU artifacts are not correlated")
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Artifact identifies one retained capture artifact and its source clock.
type Artifact struct {
	Kind          string `json:"kind"`
	RunID         string `json:"run_id"`
	ContentDigest string `json:"content_digest"`
	ClockDomain   string `json:"clock_domain"`
}

// ClockBridge maps timestamps from the host clock to the GPU clock.
type ClockBridge struct {
	HostClock    string  `json:"host_clock"`
	GPUClock     string  `json:"gpu_clock"`
	Scale        float64 `json:"scale"`
	Offset       float64 `json:"offset"`
	MaxErrorNS   float64 `json:"max_error_ns"`
	SourceDigest string  `json:"source_digest"`
}

// Receipt records the evidence required to authorize a temporal join.
type Receipt struct {
	Schema string       `json:"schema"`
	Host   Artifact     `json:"host"`
	GPU    Artifact     `json:"gpu"`
	Bridge *ClockBridge `json:"bridge,omitempty"`
}

// Validate reports whether the receipt authorizes a host-to-GPU temporal join.
func (r Receipt) Validate() error {
	if r.Schema != Schema {
		return fmt.Errorf("%w: unsupported schema %q", ErrInvalid, r.Schema)
	}
	if err := validateArtifact(r.Host, "host-signpost"); err != nil {
		return err
	}
	if err := validateArtifact(r.GPU, "gpu-trace"); err != nil {
		return err
	}
	if r.Host.RunID != r.GPU.RunID {
		return fmt.Errorf("%w: run identity differs", ErrUncorrelated)
	}
	if r.Host.ClockDomain == r.GPU.ClockDomain {
		if r.Bridge != nil {
			return fmt.Errorf("%w: bridge supplied for identical clocks", ErrInvalid)
		}
		return nil
	}
	if r.Bridge == nil {
		return fmt.Errorf("%w: clock domains differ without a bridge", ErrUncorrelated)
	}
	b := r.Bridge
	if b.HostClock != r.Host.ClockDomain || b.GPUClock != r.GPU.ClockDomain {
		return fmt.Errorf("%w: bridge clock identity differs", ErrUncorrelated)
	}
	if !finite(b.Scale) || b.Scale <= 0 || !finite(b.Offset) ||
		!finite(b.MaxErrorNS) || b.MaxErrorNS < 0 || !validDigest(b.SourceDigest) {
		return fmt.Errorf("%w: malformed clock bridge", ErrInvalid)
	}
	return nil
}

// Canonical returns the canonical JSON encoding of a valid receipt.
func (r Receipt) Canonical() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

// Read reads a bounded canonical receipt from path.
func Read(path string) (Receipt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, fmt.Errorf("read host correlation: %w", err)
	}
	if len(data) > maximumBytes {
		return Receipt{}, fmt.Errorf("%w: receipt exceeds %d bytes", ErrInvalid, maximumBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, fmt.Errorf("%w: decode receipt: %v", ErrInvalid, err)
	}
	canonical, err := receipt.Canonical()
	if err != nil {
		return Receipt{}, err
	}
	if !bytes.Equal(data, canonical) {
		return Receipt{}, fmt.Errorf("%w: receipt is not canonical", ErrInvalid)
	}
	return receipt, nil
}

func validateArtifact(a Artifact, kind string) error {
	if a.Kind != kind || a.RunID == "" || a.RunID != strings.TrimSpace(a.RunID) || len(a.RunID) > 256 ||
		a.ClockDomain == "" || a.ClockDomain != strings.TrimSpace(a.ClockDomain) ||
		len(a.ClockDomain) > 256 || !validDigest(a.ContentDigest) {
		return fmt.Errorf("%w: malformed %s artifact", ErrInvalid, kind)
	}
	return nil
}

func validDigest(s string) bool {
	return digestPattern.MatchString(s) && s != "sha256:"+strings.Repeat("0", 64)
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
