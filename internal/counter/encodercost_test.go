package counter

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// oracleExecutionCosts reads the Execution Cost column of an Xcode
// compute-kernel encoder export, in encoder order.
func oracleExecutionCosts(t *testing.T, path string) []float64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open oracle: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var costs []float64
	for line := 0; sc.Scan(); line++ {
		if line == 0 {
			continue // header
		}
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 3 {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(fields[2]), "%"), 64)
		if err != nil {
			continue
		}
		costs = append(costs, v)
	}
	return costs
}

// TestEncoderCostsAgainstXcode measures EncoderCosts against Xcode's own
// export of the same capture. The bounds are the measured residuals with room
// to move, not a claim of exactness: the method is known to differ from
// Xcode's column by up to ~0.9 pp. A regression that broke the ordinal
// placement or the cycle column would blow past them by an order of magnitude.
//
// Set GPUTRACE_TEST_GPUPROFILER_DIR to the .gpuprofiler_raw directory of the
// capture described in testdata/xcode-oracle/PROVENANCE.md.
func TestEncoderCostsAgainstXcode(t *testing.T) {
	dir := os.Getenv("GPUTRACE_TEST_GPUPROFILER_DIR")
	if dir == "" {
		t.Skip("set GPUTRACE_TEST_GPUPROFILER_DIR to a .gpuprofiler_raw directory")
	}
	stats, err := ParseStreamData(dir, nil)
	if err != nil {
		t.Fatalf("ParseStreamData: %v", err)
	}
	costs := stats.CounterArchive.EncoderCosts()
	if len(costs) == 0 {
		t.Fatal("no per-encoder execution cost")
	}

	var total float64
	for _, c := range costs {
		total += c.CostPercent
	}
	if math.Abs(total-100) > 0.01 {
		t.Errorf("costs sum to %.4f%%, want 100%%", total)
	}

	for i, c := range costs {
		if c.Ordinal != i {
			t.Errorf("cost %d has ordinal %d: encoders are not contiguous in execution order", i, c.Ordinal)
		}
		if c.Sparse() {
			t.Errorf("encoder %d rests on %d end records", c.Ordinal, c.EndRecords)
		}
	}

	oracle := oracleExecutionCosts(t, "../../testdata/xcode-oracle/compute-kernel-encoders.txt")
	if len(costs) != len(oracle) {
		t.Skipf("capture has %d encoders, oracle has %d: not the oracle's capture", len(costs), len(oracle))
	}

	var maxRes, sumSq float64
	for i, c := range costs {
		r := c.CostPercent - oracle[i]
		t.Logf("encoder %2d: %7.3f%% xcode %7.3f%% residual %+6.3f pp (%d end records, %d samples)",
			c.Ordinal, c.CostPercent, oracle[i], r, c.EndRecords, c.SampleCount)
		if math.Abs(r) > maxRes {
			maxRes = math.Abs(r)
		}
		sumSq += r * r
	}
	rms := math.Sqrt(sumSq / float64(len(costs)))
	t.Logf("max |residual| %.3f pp, rms %.3f pp", maxRes, rms)
	if maxRes > 1.5 {
		t.Errorf("max residual %.3f pp exceeds 1.5 pp; measured 0.911 pp", maxRes)
	}
	if rms > 0.5 {
		t.Errorf("rms residual %.3f pp exceeds 0.5 pp; measured 0.278 pp", rms)
	}
}
