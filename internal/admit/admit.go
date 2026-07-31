// Package admit checks whether a profiled export may be used as evidence for
// a measured-timing claim about a raw capture.
//
// The criteria are not new. They were checked by reading the output of several
// commands and comparing numbers by eye, which is how a window carrying no
// profiled data once passed a verification step: each command answered its own
// question correctly and nobody held the answers together. One gate with one
// implementation is harder to get partially right.
package admit

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/profilerraw"
	"github.com/tmc/gputrace/internal/trace"
	"github.com/tmc/gputrace/internal/tracebundle"
)

// A Criterion is one admissibility question and its answer.
type Criterion struct {
	Name    string
	Pass    bool
	Detail  string
	Blocked bool // the question could not be asked, which is not a pass
}

// Result is the verdict for a raw/profiled pair.
type Result struct {
	RawPath      string
	ProfiledPath string
	Criteria     []Criterion
}

// Admitted reports whether every criterion passed. A blocked criterion is not
// a pass: an unanswerable question leaves the claim unsupported.
func (r Result) Admitted() bool {
	for _, c := range r.Criteria {
		if !c.Pass {
			return false
		}
	}
	return len(r.Criteria) > 0
}

// Check evaluates every criterion for a raw capture and its profiled export.
// It always returns a Result: a criterion that cannot be evaluated is recorded
// as blocked rather than aborting, so one missing file does not hide the
// remaining answers.
func Check(rawPath, profiledPath string) Result {
	result := Result{RawPath: rawPath, ProfiledPath: profiledPath}
	result.Criteria = append(result.Criteria,
		checkIdentity(rawPath, profiledPath),
		checkStreamData(profiledPath),
		checkSelfContained(profiledPath),
		checkCounts(rawPath, profiledPath),
		checkTimingProvenance(profiledPath),
	)
	return result
}

func checkIdentity(rawPath, profiledPath string) Criterion {
	c := Criterion{Name: "exported UUID matches raw"}
	raw, err := trace.ReadMetadata(rawPath)
	if err != nil {
		c.Blocked = true
		c.Detail = fmt.Sprintf("read raw identity: %v", err)
		return c
	}
	exported, err := trace.ReadMetadata(profiledPath)
	if err != nil {
		c.Blocked = true
		c.Detail = fmt.Sprintf("read exported identity: %v", err)
		return c
	}
	switch {
	case raw.UUID == "" || exported.UUID == "":
		c.Blocked = true
		c.Detail = fmt.Sprintf("identity missing (raw %q, exported %q)", raw.UUID, exported.UUID)
	case raw.UUID != exported.UUID:
		c.Detail = fmt.Sprintf("exported %s is not raw %s", exported.UUID, raw.UUID)
	default:
		c.Pass = true
		c.Detail = raw.UUID
	}
	return c
}

func checkStreamData(profiledPath string) Criterion {
	c := Criterion{Name: "streamData present and non-empty"}
	dir := profilerDir(profiledPath)
	if dir == "" {
		c.Detail = "no .gpuprofiler_raw directory"
		return c
	}
	info, err := os.Stat(filepath.Join(dir, "streamData"))
	switch {
	case err != nil:
		c.Detail = fmt.Sprintf("streamData: %v", err)
	case info.Size() == 0:
		c.Detail = "streamData is empty"
	default:
		c.Pass = true
		c.Detail = fmt.Sprintf("%d bytes", info.Size())
	}
	return c
}

func checkSelfContained(profiledPath string) Criterion {
	c := Criterion{Name: "payload self-contained"}
	payload, err := tracebundle.InspectPayload(profiledPath)
	if err != nil {
		c.Blocked = true
		c.Detail = fmt.Sprintf("inspect payload: %v", err)
		return c
	}
	c.Detail = string(payload.Class)
	c.Pass = payload.Class == tracebundle.PayloadFull || payload.Class == tracebundle.PayloadProfilerOnly
	return c
}

// checkCounts compares the structural work in the raw capture against what the
// profiled export measured. A replay that recorded a different number of
// dispatches did not measure the same run.
func checkCounts(rawPath, profiledPath string) Criterion {
	c := Criterion{Name: "dispatch count matches raw"}
	raw, err := trace.Open(rawPath)
	if err != nil {
		c.Blocked = true
		c.Detail = fmt.Sprintf("open raw capture: %v", err)
		return c
	}
	rawDispatches, err := raw.CountDispatchCalls()
	if err != nil {
		c.Blocked = true
		c.Detail = fmt.Sprintf("count raw dispatches: %v", err)
		return c
	}
	dir := profilerDir(profiledPath)
	if dir == "" {
		c.Blocked = true
		c.Detail = "no .gpuprofiler_raw directory"
		return c
	}
	stats, err := counter.ParseStreamData(dir, nil)
	if err != nil {
		c.Blocked = true
		c.Detail = fmt.Sprintf("parse streamData: %v", err)
		return c
	}
	if rawDispatches != stats.NumGPUCommands {
		c.Detail = fmt.Sprintf("raw has %d dispatches, export measured %d", rawDispatches, stats.NumGPUCommands)
		return c
	}
	c.Pass = true
	c.Detail = fmt.Sprintf("%d dispatches", rawDispatches)
	return c
}

// checkTimingProvenance requires that the export's timing came from the
// profiler rather than a synthetic or capture-derived fallback. The fallbacks
// exist for visualization and are not measurements.
func checkTimingProvenance(profiledPath string) Criterion {
	c := Criterion{Name: "timing provenance is measured"}
	dir := profilerDir(profiledPath)
	if dir == "" {
		c.Detail = "no .gpuprofiler_raw directory"
		return c
	}
	stats, err := counter.ParseStreamData(dir, nil)
	if err != nil {
		c.Blocked = true
		c.Detail = fmt.Sprintf("parse streamData: %v", err)
		return c
	}
	if stats.TimingSource == "" {
		c.Detail = "no timing source recorded"
		return c
	}
	c.Pass = true
	c.Detail = stats.TimingSource
	return c
}

func profilerDir(path string) string {
	return profilerraw.FindDirWithStreamData(path)
}
