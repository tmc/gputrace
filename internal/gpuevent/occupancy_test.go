package gpuevent

import "testing"

func TestTheoreticalOccupancyRegisterLimited(t *testing.T) {
	// 255 regs/thread, block 128: regs/block = 32768 -> 2 blocks by regs
	// (64K regfile), 12 by threads, 24 cap. 2 blocks * 4 warps = 8 warps
	// of 48 max = ~16.7%.
	e := Event{
		Kind:      KindKernel,
		Block:     "128x1x1",
		Registers: 255,
	}
	pct, limiter := TheoreticalOccupancy(e)
	if limiter != "registers" {
		t.Errorf("limiter = %q, want registers", limiter)
	}
	if pct < 16 || pct > 17 {
		t.Errorf("pct = %.1f, want ~16.7", pct)
	}
}

func TestTheoreticalOccupancySharedMemoryLimited(t *testing.T) {
	// 100KB shared/block: 2 blocks fit in 228KB; threads would allow more.
	e := Event{
		Kind:      KindKernel,
		Block:     "256x1x1",
		Registers: 32,
		SharedMem: 100 * 1024,
	}
	pct, limiter := TheoreticalOccupancy(e)
	if limiter != "shared-memory" {
		t.Errorf("limiter = %q, want shared-memory", limiter)
	}
	// 2 blocks * 8 warps = 16/48 = 33.3%
	if pct < 33 || pct > 34 {
		t.Errorf("pct = %.1f, want ~33.3", pct)
	}
}

func TestTheoreticalOccupancyThreadLimited(t *testing.T) {
	e := Event{Kind: KindKernel, Block: "384x1x1", Registers: 32}
	pct, limiter := TheoreticalOccupancy(e)
	if limiter != "threads" {
		t.Errorf("limiter = %q, want threads", limiter)
	}
	// 1536/384 = 4 blocks * 12 warps = 48/48 = 100%
	if pct != 100 {
		t.Errorf("pct = %.1f, want 100", pct)
	}
}

func TestOccupancyMissingAttributes(t *testing.T) {
	if _, limiter := TheoreticalOccupancy(Event{Kind: KindKernel}); limiter != "" {
		t.Errorf("empty event should report no occupancy, got %q", limiter)
	}
}

func TestAnalyzePageableTransfers(t *testing.T) {
	events := []Event{
		{Kind: KindMemcpy, SrcKind: "pageable", DstKind: "device", StartNS: 0, EndNS: 900, Bytes: 400},
		{Kind: KindMemcpy, SrcKind: "device", DstKind: "pinned", StartNS: 1000, EndNS: 1100},
	}
	st := AnalyzePageableTransfers(events)
	if st.CopyCount != 2 {
		t.Errorf("copy_count = %d", st.CopyCount)
	}
	if st.PageableNS != 900 || st.TotalCopyNS != 1000 {
		t.Errorf("pageable=%d total=%d", st.PageableNS, st.TotalCopyNS)
	}
	if st.PageablePct < 89.9 || st.PageablePct > 90.1 {
		t.Errorf("pageable_pct = %.1f", st.PageablePct)
	}
}
