package hostcorrelation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Receipt)
		want error
	}{
		{"same clock", func(*Receipt) {}, nil},
		{"different run", func(r *Receipt) { r.GPU.RunID = "other" }, ErrUncorrelated},
		{"missing bridge", func(r *Receipt) { r.GPU.ClockDomain = "gpu" }, ErrUncorrelated},
		{"unnecessary bridge", func(r *Receipt) { r.Bridge = validBridge("host") }, ErrInvalid},
		{"wrong bridge clock", func(r *Receipt) { r.GPU.ClockDomain = "gpu"; r.Bridge = validBridge("other") }, ErrUncorrelated},
		{"zero scale", func(r *Receipt) { r.GPU.ClockDomain = "gpu"; r.Bridge = validBridge("gpu"); r.Bridge.Scale = 0 }, ErrInvalid},
		{"uppercase digest", func(r *Receipt) { r.Host.ContentDigest = strings.ToUpper(digestA) }, ErrInvalid},
		{"zero digest", func(r *Receipt) { r.Host.ContentDigest = "sha256:" + strings.Repeat("0", 64) }, ErrInvalid},
		{"whitespace run", func(r *Receipt) { r.Host.RunID = " run-1" }, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := validReceipt()
			tt.edit(&receipt)
			err := receipt.Validate()
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestReadRequiresCanonicalJSON(t *testing.T) {
	receipt := validReceipt()
	data, err := receipt.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Read(noncanonical) error = %v, want ErrInvalid", err)
	}
}

func TestRetainedSeparateRunsRefuse(t *testing.T) {
	// The fixture binds the retained Xcode Logging and Metal System Trace
	// artifacts from docs/research/CONTROLLED_PARITY_CAPTURE.md. They contain
	// complementary evidence, but came from different executions.
	_, err := Read(filepath.Join("testdata", "separate-runs.json"))
	if !errors.Is(err, ErrUncorrelated) {
		t.Fatalf("Read(separate runs) error = %v, want ErrUncorrelated", err)
	}
}

func validReceipt() Receipt {
	return Receipt{
		Schema: Schema,
		Host:   Artifact{Kind: "host-signpost", RunID: "run-1", ContentDigest: digestA, ClockDomain: "host"},
		GPU:    Artifact{Kind: "gpu-trace", RunID: "run-1", ContentDigest: digestB, ClockDomain: "host"},
	}
}

func validBridge(gpuClock string) *ClockBridge {
	return &ClockBridge{
		HostClock: "host", GPUClock: gpuClock, Scale: 1, Offset: 10,
		MaxErrorNS: 100, SourceDigest: digestA,
	}
}
