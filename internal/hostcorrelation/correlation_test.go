package hostcorrelation

import (
	"errors"
	"math"
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
		{"two samples", func(r *Receipt) {
			r.GPU.ClockDomain = "gpu"
			r.Bridge = validBridge("gpu")
			r.Bridge.Samples = r.Bridge.Samples[:2]
		}, ErrInvalid},
		{"unordered host samples", func(r *Receipt) {
			r.GPU.ClockDomain = "gpu"
			r.Bridge = validBridge("gpu")
			r.Bridge.Samples[1].HostNS = 0
		}, ErrInvalid},
		{"unordered GPU samples", func(r *Receipt) {
			r.GPU.ClockDomain = "gpu"
			r.Bridge = validBridge("gpu")
			r.Bridge.Samples[1].GPUNS = 0
		}, ErrInvalid},
		{"uppercase digest", func(r *Receipt) { r.Host.ContentDigest = strings.ToUpper(digestA) }, ErrInvalid},
		{"zero digest", func(r *Receipt) { r.Host.ContentDigest = "sha256:" + strings.Repeat("0", 64) }, ErrInvalid},
		{"whitespace run", func(r *Receipt) { r.Host.RunID = " run-1" }, ErrInvalid},
		{"duplicate event", func(r *Receipt) { r.Events = append(r.Events, r.Events[0]) }, ErrInvalid},
		{"unordered event", func(r *Receipt) {
			r.Events = append(r.Events, Event{ID: "earlier", Kind: "instant", Name: "earlier", TimestampNS: 1})
		}, ErrInvalid},
		{"instant duration", func(r *Receipt) { r.Events[0].DurationNS = 1 }, ErrInvalid},
		{"event outside bridge", func(r *Receipt) {
			r.GPU.ClockDomain = "gpu"
			r.Bridge = validBridge("gpu")
			r.Events[0].TimestampNS = 101
		}, ErrUncorrelated},
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

func TestProject(t *testing.T) {
	receipt := validReceipt()
	receipt.GPU.ClockDomain = "gpu"
	receipt.Bridge = validBridge("gpu")
	receipt.Events = []Event{{ID: "encode", Kind: "interval", Name: "Encode", TimestampNS: 10, DurationNS: 4}}
	got, err := receipt.Project()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TimestampNS != 25 || got[0].DurationNS != 8 || got[0].MaxErrorNS != 7 {
		t.Fatalf("Project() = %+v, want timestamp=25 duration=8 error=7", got)
	}
}

func TestProjectRejectsOverflow(t *testing.T) {
	receipt := validReceipt()
	receipt.Events[0].TimestampNS = math.MaxInt64
	receipt.GPU.ClockDomain = "gpu"
	receipt.Bridge = validBridge("gpu")
	if _, err := receipt.Project(); !errors.Is(err, ErrUncorrelated) {
		t.Fatalf("Project() error = %v, want ErrUncorrelated", err)
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
		Events: []Event{{ID: "complete", Kind: "instant", Name: "Complete", TimestampNS: 10}},
	}
}

func validBridge(gpuClock string) *ClockBridge {
	return &ClockBridge{
		HostClock: "host", GPUClock: gpuClock, SourceDigest: digestA,
		Samples: []ClockSample{
			{HostNS: 0, GPUNS: 5},
			{HostNS: 50, GPUNS: 112},
			{HostNS: 100, GPUNS: 205},
		},
	}
}
