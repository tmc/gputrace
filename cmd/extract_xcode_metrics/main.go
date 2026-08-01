//go:build darwin

// Package main provides a CLI tool to extract high-level Xcode GPU profiler workload metrics and timeline counters safely.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"unsafe"

	puregoobjc "github.com/ebitengine/purego/objc"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objc/objcinspect"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

func check(id objc.ID, selector string, want reflect.Type, args ...any) {
	if err := objcinspect.Check(puregoobjc.ID(uintptr(id)), puregoobjc.RegisterName(selector), want, args...); err != nil {
		fmt.Fprintf(os.Stderr, "Error: type check failed for selector %s: %v\n", selector, err)
		os.Exit(1)
	}
}

type CounterStreamSummary struct {
	Name           string
	MaxVal         float64
	AvgVal         float64
	Count          uint64
	FirstTimestamp uint64
	LastTimestamp  uint64
	SampleInterval uint64
	Scope          uint16
	ScopeIndex     uint64
	IsHex          bool
	IsUnread       bool
	UnreadNote     string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: extract_xcode_metrics <path-to-.gputrace-directory>\n")
		os.Exit(1)
	}

	inputPath := os.Args[1]
	absPath, err := filepath.Abs(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: resolving absolute path for %s: %v\n", inputPath, err)
		os.Exit(1)
	}
	if resolvedPath, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolvedPath
	}

	// Resolve directory vs streamData file path
	var archiveDir string
	if fi, err := os.Stat(absPath); err == nil && !fi.IsDir() {
		if filepath.Base(absPath) == "streamData" {
			archiveDir = filepath.Dir(filepath.Dir(absPath))
		} else {
			archiveDir = filepath.Dir(absPath)
		}
	} else {
		archiveDir = absPath
	}

	// Resolve raw streamData file path inside directory
	var streamDataFile string
	err = filepath.Walk(archiveDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Base(p) == "streamData" {
			streamDataFile = p
			return filepath.SkipAll
		}
		return nil
	})

	if streamDataFile == "" {
		fmt.Fprintf(os.Stderr, "Error: could not resolve .gpuprofiler_raw/streamData under %s\n", archiveDir)
		os.Exit(1)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	os.Setenv("GPUTRACE_XCODE_APP", "/Applications/Xcode-rc.app")

	rawBytes, err := os.ReadFile(streamDataFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: reading streamData at %s: %v\n", streamDataFile, err)
		os.Exit(1)
	}

	dataObj := foundation.NewDataWithBytesLength(rawBytes)
	targetCls := gtshaderprofiler.GetGTShaderProfilerStreamDataClass().Class()

	unarchived, err := foundation.GetNSKeyedUnarchiverClass().
		UnarchivedObjectOfClassFromDataError(targetCls, dataObj)
	if err != nil || unarchived.GetID() == 0 {
		fmt.Fprintf(os.Stderr, "Error: unarchiving streamData: %v\n", err)
		os.Exit(1)
	}

	stream := gtshaderprofiler.GTShaderProfilerStreamDataFromID(unarchived.GetID())

	// Call _setupDataPath on streamData object to bind directory dependencies
	check(stream.GetID(), "_setupDataPath", reflect.TypeOf(objc.ID(0)))
	objc.Send[objc.ID](stream.GetID(), objc.Sel("_setupDataPath"))

	procClassObj := objc.GetClass("GTShaderProfilerStreamDataProcessor")
	procAlloc := objc.Send[objc.ID](objc.ID(procClassObj), objc.Sel("alloc"))

	check(procAlloc, "initWithStreamData:llvmHelperPath:", reflect.TypeOf(objc.ID(0)), stream.GetID(), objc.ID(0))
	procObjID := objc.Send[objc.ID](procAlloc, objc.Sel("initWithStreamData:llvmHelperPath:"), stream.GetID(), 0)
	procObj := gtshaderprofiler.GTShaderProfilerStreamDataProcessorFromID(procObjID)

	check(procObj.GetID(), "processStreamData", nil)
	procObj.ProcessStreamData()

	mID := procObj.MioData()
	check(mID.GetID(), "gpuTime", reflect.TypeOf(uint64(0)))
	check(mID.GetID(), "encoderCount", reflect.TypeOf(uint64(0)))
	check(mID.GetID(), "drawCount", reflect.TypeOf(uint64(0)))
	check(mID.GetID(), "pipelineStateCount", reflect.TypeOf(uint64(0)))

	gpuTimeNS := mID.GpuTime()
	gpuTimeMS := float64(gpuTimeNS) / 1e6

	// Extract timeline counters off nonOverlappingTimeline
	var summaries []CounterStreamSummary
	nonOverlappingPtr := mID.NonOverlappingTimeline()
	if nonOverlappingPtr != nil {
		nonOverlappingID := objc.ID(uintptr(nonOverlappingPtr))
		check(nonOverlappingID, "timelineCounters", reflect.TypeOf(objc.ID(0)))
		countersObj := gtshaderprofiler.GTMioTraceTimelineDataFromID(nonOverlappingID).TimelineCounters()

		if countersObj.GetID() != 0 {
			check(countersObj.GetID(), "counters", reflect.TypeOf(objc.ID(0)))
			dictObj := countersObj.Counters()
			if dictObj.GetID() != 0 {
				check(dictObj.GetID(), "allKeys", reflect.TypeOf(objc.ID(0)))
				keys := dictObj.AllKeys()
				for _, key := range keys {
					kStr := foundation.NSStringFromID(key.GetID()).String()
					cntObj := dictObj.ObjectForKey(key)
					cnt := gtshaderprofiler.GTMioCounterDataFromID(cntObj.GetID())
					check(cnt.GetID(), "sampleCount", reflect.TypeOf(uint64(0)))
					check(cnt.GetID(), "values", reflect.TypeOf(unsafe.Pointer(nil)))
					check(cnt.GetID(), "timestamps", reflect.TypeOf(unsafe.Pointer(nil)))
					check(cnt.GetID(), "sampleInterval", reflect.TypeOf(uint64(0)))
					check(cnt.GetID(), "scope", reflect.TypeOf(uint16(0)))
					check(cnt.GetID(), "scopeIndex", reflect.TypeOf(uint64(0)))

					// Seed the extremes from the data, not from zero: a series
					// that never rises above zero would otherwise report a max
					// of zero regardless of what it holds.
					vals := cnt.ValuesSlice()
					var minV, maxV, sumV float64
					for i, v := range vals {
						if i == 0 {
							minV, maxV = v, v
						}
						minV = min(minV, v)
						maxV = max(maxV, v)
						sumV += v
					}
					avgV := 0.0
					if len(vals) > 0 {
						avgV = sumV / float64(len(vals))
					}
					stamps := cnt.TimestampsSlice()
					if len(stamps) != len(vals) {
						fmt.Fprintf(os.Stderr, "Error: %s timestamps=%d values=%d\n", kStr, len(stamps), len(vals))
						os.Exit(1)
					}
					var first, last uint64
					if len(stamps) > 0 {
						first, last = stamps[0], stamps[len(stamps)-1]
					}

					isHex := false
					if len(kStr) == 16 || len(kStr) == 64 {
						isHex = true
					}

					// Two different reasons to withhold a column, and only one of
					// them is a property of the name. Whether a series is all
					// zero is a property of the data, so measure it rather than
					// listing the three counters that happened to be empty here;
					// a hardcoded list keeps suppressing them once they carry
					// values, and stays quiet when a fourth goes empty.
					isUnread := false
					unreadNote := ""
					switch {
					case kStr == "Texture Read Limiter":
						isUnread = true
						unreadNote = " (Unread: unestablished encoding, max 8.99e10 vs Xcode oracle 0.00%)"
					case len(vals) > 0 && maxV == 0 && minV == 0:
						isUnread = true
						unreadNote = " (Unread: every sample zero)"
					}

					summaries = append(summaries, CounterStreamSummary{
						Name:           kStr,
						MaxVal:         maxV,
						AvgVal:         avgV,
						Count:          cnt.SampleCount(),
						FirstTimestamp: first,
						LastTimestamp:  last,
						SampleInterval: cnt.SampleInterval(),
						Scope:          cnt.Scope(),
						ScopeIndex:     cnt.ScopeIndex(),
						IsHex:          isHex,
						IsUnread:       isUnread,
						UnreadNote:     unreadNote,
					})
				}
			}
		}
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})

	fmt.Println("==========================================================================")
	fmt.Printf("   RESOLVED ARCHIVE PATH: %s\n", archiveDir)
	fmt.Println("==========================================================================")
	fmt.Printf("[1. Workload Summary]\n")
	fmt.Printf("  Compute Encoder Count:        %d\n", mID.EncoderCount())
	fmt.Printf("  Compute Dispatch Count:       %d\n", mID.DrawCount())
	fmt.Printf("  Pipeline State Count:         %d\n", mID.PipelineStateCount())
	fmt.Printf("  GPU Time:                     %.3f ms\n", gpuTimeMS)

	fmt.Printf("\n[2. Memory Timeline Counters (%d Channels Extracted)]\n", len(summaries))
	for _, s := range summaries {
		if s.IsUnread {
			fmt.Printf("  %-36s -> [Unread / Encoding Unestablished]%s (samples: %d, scope: %d/%d, interval: %d, ticks: %d..%d)\n", s.Name, s.UnreadNote, s.Count, s.Scope, s.ScopeIndex, s.SampleInterval, s.FirstTimestamp, s.LastTimestamp)
		} else {
			fmt.Printf("  %-36s -> Peak: %-10.2f Avg: %-10.2f (samples: %d, scope: %d/%d, interval: %d, ticks: %d..%d)\n", s.Name, s.MaxVal, s.AvgVal, s.Count, s.Scope, s.ScopeIndex, s.SampleInterval, s.FirstTimestamp, s.LastTimestamp)
		}
	}

	fmt.Println("==========================================================================")
}
