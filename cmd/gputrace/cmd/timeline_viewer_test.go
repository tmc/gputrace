package cmd

import (
	"strings"
	"testing"
)

func TestLoopbackListenAddress(t *testing.T) {
	for _, test := range []struct {
		address string
		want    bool
	}{
		{"127.0.0.1:0", true},
		{"[::1]:1234", true},
		{"0.0.0.0:8080", false},
		{"localhost:8080", false},
		{"bad", false},
	} {
		if got := loopbackListenAddress(test.address); got != test.want {
			t.Errorf("loopbackListenAddress(%q) = %v, want %v", test.address, got, test.want)
		}
	}
}

func TestResolveTimelineNavigation(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{
		{Name: "rms", Category: "kernel", Timestamp: 10, Duration: 2},
		{Name: "rms", Category: "kernel", Timestamp: 20, Duration: 3},
	}}
	opts := &timelineOptions{serveViewer: true, kernel: "rms", kernelOccurrence: -1}
	if err := resolveTimelineNavigation(timeline, opts); err == nil || !strings.Contains(err.Error(), "2 occurrences") {
		t.Fatalf("ambiguous kernel error = %v", err)
	}
	opts.kernelOccurrence = 1
	if err := resolveTimelineNavigation(timeline, opts); err != nil {
		t.Fatal(err)
	}
	if opts.selectionStartNS != 20_000 || opts.selectionDurationNS != 3_000 {
		t.Fatalf("selection = %d+%d ns", opts.selectionStartNS, opts.selectionDurationNS)
	}
	if opts.navigationStartNS >= opts.selectionStartNS || opts.navigationEndNS <= opts.selectionStartNS+opts.selectionDurationNS {
		t.Fatalf("viewport = [%d,%d], selection = [%d,%d]", opts.navigationStartNS, opts.navigationEndNS, opts.selectionStartNS, opts.selectionStartNS+opts.selectionDurationNS)
	}
}

func TestResolveTimelineNavigationTimeRange(t *testing.T) {
	opts := &timelineOptions{serveViewer: true, kernelOccurrence: -1, timeStart: 1.25, timeEnd: 2.5}
	if err := resolveTimelineNavigation(&Timeline{}, opts); err != nil {
		t.Fatal(err)
	}
	if opts.navigationStartNS != 1_250_000_000 || opts.navigationEndNS != 2_500_000_000 {
		t.Fatalf("navigation = [%d,%d]", opts.navigationStartNS, opts.navigationEndNS)
	}
}

func TestValidateTimelineViewerOptions(t *testing.T) {
	valid := &timelineOptions{format: "perfetto", clock: timelineClockBusy, serveViewer: true, remoteUI: true, listen: "127.0.0.1:0", kernelOccurrence: -1}
	if err := validateTimelineViewerOptions(valid, "trace.pftrace"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		opts timelineOptions
	}{
		{"two actions", timelineOptions{format: "perfetto", openViewer: true, serveViewer: true, remoteUI: true, listen: "127.0.0.1:0"}},
		{"wrong format", timelineOptions{format: "chrome", serveViewer: true, remoteUI: true, listen: "127.0.0.1:0"}},
		{"no UI", timelineOptions{format: "perfetto", serveViewer: true, listen: "127.0.0.1:0"}},
		{"public", timelineOptions{format: "perfetto", serveViewer: true, remoteUI: true, listen: "0.0.0.0:1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateTimelineViewerOptions(&test.opts, "trace.pftrace"); err == nil {
				t.Fatal("invalid viewer options accepted")
			}
		})
	}
}
