//go:build darwin

package counter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestDecodeAPSCounterShardPopulation decodes every Counters_f_*.raw in a
// directory. The GPU tuple is mandatory so the test cannot silently substitute
// the developer's current GPU for the capture's GPU.
//
// Reproduce the parity-asymmetric-perfdata result with:
//
//	GPUTRACE_PROBE_COUNTERS_DIR=path/to/.gpuprofiler_raw \
//	GPUTRACE_PROBE_GPU=16,6,1,0 \
//	go test ./internal/counter -run TestDecodeAPSCounterShardPopulation -v
func TestDecodeAPSCounterShardPopulation(t *testing.T) {
	dir := os.Getenv("GPUTRACE_PROBE_COUNTERS_DIR")
	if dir == "" {
		t.Skip("set GPUTRACE_PROBE_COUNTERS_DIR and GPUTRACE_PROBE_GPU")
	}
	var config APSGPUConfig
	if n, err := fmt.Sscanf(os.Getenv("GPUTRACE_PROBE_GPU"), "%d,%d,%d,%d",
		&config.Generation, &config.Variant, &config.Revision, &config.CounterUarchBehaviour); err != nil || n != 4 {
		t.Fatalf("GPUTRACE_PROBE_GPU must be generation,variant,revision,uarch: parsed %d fields: %v", n, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "Counters_f_") && strings.HasSuffix(entry.Name(), ".raw") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no Counters_f_*.raw files in %s", dir)
	}
	var decoded, descents int
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		shard, err := DecodeAPSCounterShard(data, config)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if len(shard.Series) == 0 {
			t.Errorf("%s: no counter series", file)
			continue
		}
		if len(shard.SystemTimestamps) == 0 {
			t.Errorf("%s: no system timestamps", file)
			continue
		}
		decoded++
		descents += shard.TimestampDescents
		t.Logf("%s: series=%d timestamps=%d descents=%d kicks=%d tokens=%d bits=%d",
			file, len(shard.Series), len(shard.SystemTimestamps), shard.TimestampDescents,
			shard.KickCount, shard.ParsedTokens, shard.ParsedBits)
	}
	t.Logf("population: files=%d decoded=%d timestampDescents=%d", len(files), decoded, descents)
	if decoded != len(files) {
		t.Fatalf("decoded %d of %d counter shards", decoded, len(files))
	}
}
