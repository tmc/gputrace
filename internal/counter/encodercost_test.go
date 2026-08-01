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
// to move, not a claim of exactness. How far off the method runs depends on the
// capture: 0.911 pp worst-case on the 23-encoder export, 2.941 pp on the
// 11-encoder one. A regression that broke the ordinal placement or the cycle
// column would blow past either by an order of magnitude.
//
// The 11-encoder capture is where the method's shape shows. Its error is
// concentrated almost entirely on encoder 9 (-2.941 pp), the encoder Xcode puts
// at 20.582%, three times its nearest rival, off four dispatches at 53%
// occupancy where every other encoder sits near 14%. Because the costs are
// shares constrained to sum to 100%, understating that one encoder is what
// pushes the other ten uniformly positive. Treat the residual as one error with
// a redistributed remainder, not eleven independent ones.
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

	// Two captures have Xcode exports, and the method is three times worse on
	// the second than on the first, so the bounds are per capture. A single
	// global bound would either pass the worse capture vacuously or fail the
	// better one; neither would notice a regression. Match on encoder count,
	// which is distinct across the two.
	oracles := []struct {
		path   string
		maxRes float64 // measured max |residual|, in percentage points
		rms    float64 // measured rms residual
	}{
		{"../../testdata/xcode-oracle/compute-kernel-encoders.txt", 0.911, 0.278},
		{"../../testdata/xcode-oracle-static-tokens2to3/compute-kernel.txt", 2.941, 1.034},
	}
	var oracle []float64
	var wantMaxRes, wantRMS float64
	var oraclePath string
	for _, o := range oracles {
		if candidate := oracleExecutionCosts(t, o.path); len(candidate) == len(costs) {
			oracle, oraclePath = candidate, o.path
			wantMaxRes, wantRMS = o.maxRes, o.rms
			break
		}
	}
	if oracle == nil {
		t.Skipf("capture has %d encoders; no oracle export matches", len(costs))
	}
	t.Logf("oracle %s", oraclePath)

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
	if maxRes > wantMaxRes*1.65 {
		t.Errorf("max residual %.3f pp; measured %.3f pp on this capture", maxRes, wantMaxRes)
	}
	if rms > wantRMS*1.8 {
		t.Errorf("rms residual %.3f pp; measured %.3f pp on this capture", rms, wantRMS)
	}
}
