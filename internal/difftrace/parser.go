package difftrace

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tmc/gputrace/internal/counter"
)

// LoadTraceData parses profiler dispatch data from a trace bundle.
func LoadTraceData(path string, onlyEncoder int, onlyFunction *regexp.Regexp) (*TraceData, error) {
	label := filepath.Base(path)
	out := &TraceData{Path: path, Label: label}

	profilerDir := findProfilerDir(path)
	if profilerDir == "" {
		out.Warnings = append(out.Warnings, fmt.Sprintf("no profiler data found for %s", path))
		return out, nil
	}

	stats, err := counter.ParseStreamData(profilerDir, nil)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("parse streamData failed for %s: %v", path, err))
		return out, nil
	}

	pipelineHashes := buildPipelineHashes(stats)
	out.Pipelines = buildPipelineInfo(stats, pipelineHashes)
	threadgroupSigs, sigErr := loadDispatchThreadgroupSignatures(path)
	if sigErr != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("threadgroup signatures unavailable for %s: %v", path, sigErr))
	}
	if len(threadgroupSigs) > 0 && len(stats.Dispatches) > 0 && absInt(len(threadgroupSigs)-len(stats.Dispatches)) > len(stats.Dispatches)/10 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("dispatch/threadgroup count mismatch for %s: dispatches=%d threadgroups=%d", path, len(stats.Dispatches), len(threadgroupSigs)))
	}

	out.Dispatches = sanitizeDispatches(stats, onlyEncoder, onlyFunction, pipelineHashes, threadgroupSigs)
	out.Encoders = summarizeEncoders(out.Dispatches, stats.EncoderTimings)
	if len(out.Dispatches) == 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("no dispatches after filtering in %s", path))
	}
	return out, nil
}

func buildPipelineInfo(stats *counter.StreamDataStats, hashes map[int]string) map[int]PipelineInfo {
	out := map[int]PipelineInfo{}
	if stats == nil {
		return out
	}
	for _, p := range stats.Pipelines {
		out[p.PipelineID] = PipelineInfo{
			PipelineID:   p.PipelineID,
			FunctionName: strings.TrimSpace(p.FunctionName),
			PipelineHash: hashes[p.PipelineID],
			StaticCounters: StaticCounters{
				Instructions: p.InstructionCount,
				Registers:    p.TemporaryRegisterCount,
				Loads:        p.DeviceLoadCount,
				Stores:       p.DeviceStoreCount,
			},
		}
	}
	return out
}

func sanitizeDispatches(stats *counter.StreamDataStats, onlyEncoder int, onlyFunction *regexp.Regexp, pipelineHashes map[int]string, threadgroupSigs []string) []Dispatch {
	if stats == nil || len(stats.Dispatches) == 0 {
		return nil
	}
	dispatches := make([]Dispatch, 0, len(stats.Dispatches))
	for _, d := range stats.Dispatches {
		name := strings.TrimSpace(d.FunctionName)
		if onlyEncoder >= 0 && d.EncoderIndex != onlyEncoder {
			continue
		}
		if onlyFunction != nil && !onlyFunction.MatchString(name) {
			continue
		}
		duration := d.DurationUs
		if duration < 0 {
			duration = 0
		}
		pipelineHash := pipelineHashes[d.PipelineID]
		if pipelineHash == "" {
			pipelineHash = fmt.Sprintf("pid%d", d.PipelineID)
		}
		threadgroupSig := "unknown"
		if d.Index >= 0 && d.Index < len(threadgroupSigs) && threadgroupSigs[d.Index] != "" {
			threadgroupSig = threadgroupSigs[d.Index]
		}
		kernelID := kernelIdentity(name, pipelineHash, threadgroupSig)
		key := normalizeName(name)
		if key == "" {
			key = kernelID
		}
		dispatches = append(dispatches, Dispatch{
			SourceIndex:    d.Index,
			FunctionName:   name,
			FunctionKey:    key,
			KernelID:       kernelID,
			PipelineHash:   pipelineHash,
			ThreadgroupSig: threadgroupSig,
			PipelineID:     d.PipelineID,
			PipelineIndex:  d.PipelineIndex,
			EncoderIndex:   d.EncoderIndex,
			DurationUs:     duration,
			CumulativeUs:   d.CumulativeUs,
		})
	}
	sort.Slice(dispatches, func(i, j int) bool {
		if dispatches[i].SourceIndex == dispatches[j].SourceIndex {
			if dispatches[i].EncoderIndex == dispatches[j].EncoderIndex {
				return dispatches[i].PipelineID < dispatches[j].PipelineID
			}
			return dispatches[i].EncoderIndex < dispatches[j].EncoderIndex
		}
		return dispatches[i].SourceIndex < dispatches[j].SourceIndex
	})
	return dispatches
}

func summarizeEncoders(dispatches []Dispatch, timings []counter.EncoderTimingInfo) []EncoderInfo {
	byIdx := map[int]*EncoderInfo{}
	for _, d := range dispatches {
		enc := byIdx[d.EncoderIndex]
		if enc == nil {
			enc = &EncoderInfo{Index: d.EncoderIndex}
			byIdx[d.EncoderIndex] = enc
		}
		enc.DispatchCount++
		enc.DurationUs += d.DurationUs
	}
	for _, t := range timings {
		enc := byIdx[t.Index]
		if enc == nil {
			enc = &EncoderInfo{Index: t.Index}
			byIdx[t.Index] = enc
		}
		if t.Label != "" {
			enc.Label = t.Label
		}
		if t.DurationMicros > 0 {
			enc.DurationUs = t.DurationMicros
		}
	}
	encoders := make([]EncoderInfo, 0, len(byIdx))
	for _, enc := range byIdx {
		encoders = append(encoders, *enc)
	}
	sort.Slice(encoders, func(i, j int) bool { return encoders[i].Index < encoders[j].Index })
	return encoders
}

func findProfilerDir(path string) string {
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return ""
	}

	if filepath.Ext(path) == ".gpuprofiler_raw" {
		if hasStreamData(path) {
			return path
		}
	}

	adjacent := path + ".gpuprofiler_raw"
	if hasStreamData(adjacent) {
		return adjacent
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() || filepath.Ext(entry.Name()) != ".gpuprofiler_raw" {
			continue
		}
		dir := filepath.Join(path, entry.Name())
		if hasStreamData(dir) {
			return dir
		}
	}
	return ""
}

func hasStreamData(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "streamData"))
	return err == nil
}

func buildPipelineHashes(stats *counter.StreamDataStats) map[int]string {
	out := make(map[int]string)
	if stats == nil {
		return out
	}
	for _, p := range stats.Pipelines {
		out[p.PipelineID] = hashPipelineStats(p)
	}
	return out
}

func hashPipelineStats(p counter.PipelineStats) string {
	h := fnv.New64a()
	writeString := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	writeInt := func(v int) {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], uint64(v))
		_, _ = h.Write(buf[:])
	}

	writeString(normalizeName(p.FunctionName))
	writeInt(p.TemporaryRegisterCount)
	writeInt(p.UniformRegisterCount)
	writeInt(p.SpilledBytes)
	writeInt(p.ThreadInvariantSpilled)
	writeInt(p.ThreadgroupMemory)
	writeInt(p.InstructionCount)
	writeInt(p.ALUInstructionCount)
	writeInt(p.FP32InstructionCount)
	writeInt(p.FP16InstructionCount)
	writeInt(p.INT32InstructionCount)
	writeInt(p.INT16InstructionCount)
	writeInt(p.BranchInstructionCount)
	writeInt(p.DeviceLoadCount)
	writeInt(p.DeviceStoreCount)
	writeInt(p.DeviceAtomicCount)
	writeInt(p.TextureReadCount)
	writeInt(p.TextureWriteCount)
	writeInt(p.ThreadgroupLoadCount)
	writeInt(p.ThreadgroupStoreCount)
	writeInt(p.ThreadgroupAtomicCount)
	writeInt(p.WaitInstructionCount)

	return fmt.Sprintf("%016x", h.Sum64())
}

func loadDispatchThreadgroupSignatures(tracePath string) ([]string, error) {
	capturePath := filepath.Join(tracePath, "capture")
	data, err := os.ReadFile(capturePath)
	if err != nil {
		return nil, err
	}

	marker := []byte("ul@3")
	sigs := make([]string, 0, 1024)
	offset := 0
	for {
		pos := bytes.Index(data[offset:], marker)
		if pos == -1 {
			break
		}
		abs := offset + pos
		if abs+0x41 <= len(data) {
			tx := binary.LittleEndian.Uint64(data[abs+0x11 : abs+0x19])
			ty := binary.LittleEndian.Uint64(data[abs+0x19 : abs+0x21])
			tz := binary.LittleEndian.Uint64(data[abs+0x21 : abs+0x29])
			tgx := binary.LittleEndian.Uint64(data[abs+0x29 : abs+0x31])
			tgy := binary.LittleEndian.Uint64(data[abs+0x31 : abs+0x39])
			tgz := binary.LittleEndian.Uint64(data[abs+0x39 : abs+0x41])
			sigs = append(sigs, fmt.Sprintf("%dx%dx%d/%dx%dx%d", tx, ty, tz, tgx, tgy, tgz))
		}
		offset = abs + 4
	}
	if len(sigs) == 0 {
		return nil, fmt.Errorf("no dispatch markers")
	}
	return sigs, nil
}
