package trace

import "testing"

func TestCorrelateGPUExecutionKeepsEncoderIntervals(t *testing.T) {
	const commandBufferID = 0x100

	events := []*KDebugEvent{
		kdebugEvent(10, KDebugGPUSubmission, commandBufferID, 0),
		kdebugEvent(40, KDebugGPUExecutionStart, commandBufferID, 0x20),
		kdebugEvent(55, KDebugGPUExecutionEnd, commandBufferID, 0x20),
		kdebugEvent(20, KDebugGPUExecutionStart, commandBufferID, 0x10),
		kdebugEvent(35, KDebugGPUExecutionEnd, commandBufferID, 0x10),
	}

	intervals := CorrelateGPUExecution(events)
	if len(intervals) != 2 {
		t.Fatalf("interval count = %d, want 2", len(intervals))
	}

	tests := []struct {
		index     int
		encoderID uint64
		duration  uint64
	}{
		{0, 0x10, 15},
		{1, 0x20, 15},
	}
	for _, tt := range tests {
		interval := intervals[tt.index]
		if interval.CommandBufferID != commandBufferID {
			t.Fatalf("interval %d command buffer = %#x, want %#x", tt.index, interval.CommandBufferID, commandBufferID)
		}
		if interval.EncoderID != tt.encoderID {
			t.Fatalf("interval %d encoder = %#x, want %#x", tt.index, interval.EncoderID, tt.encoderID)
		}
		if interval.SubmissionEvent == nil {
			t.Fatalf("interval %d missing submission event", tt.index)
		}
		if got := interval.Duration(); got != tt.duration {
			t.Fatalf("interval %d duration = %d, want %d", tt.index, got, tt.duration)
		}
	}
}

func kdebugEvent(timestamp uint64, subclass uint32, commandBufferID, encoderID uint64) *KDebugEvent {
	return &KDebugEvent{
		Timestamp: timestamp,
		DebugID:   KDebugClassGPU<<24 | subclass<<16,
		Args:      [4]uint64{commandBufferID, encoderID},
	}
}
