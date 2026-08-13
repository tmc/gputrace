package cmd

import "testing"

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

func TestValidateTimelineViewerOptions(t *testing.T) {
	valid := &timelineOptions{format: "perfetto", clock: timelineClockBusy, serveViewer: true, remoteUI: true, listen: "127.0.0.1:0"}
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
