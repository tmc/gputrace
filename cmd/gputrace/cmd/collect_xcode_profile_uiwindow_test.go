package cmd

import "testing"

func TestAdmitUIIdentifiedWindow(t *testing.T) {
	const unbound = "selected GPU trace window has no title or AXDocument match for the requested trace"
	tests := []struct {
		name         string
		selection    xcodeWindowSelection
		window       uintptr
		uiIdentified uintptr
		want         bool
		wantEvidence string
	}{
		{
			name:         "untitled window matched by GPU trace UI is admitted",
			selection:    xcodeWindowSelection{Evidence: unbound},
			window:       0x40,
			uiIdentified: 0x40,
			want:         true,
			wantEvidence: "sole window with GPU trace UI in the bound Xcode process",
		},
		{
			name:         "a different window is not admitted",
			selection:    xcodeWindowSelection{Evidence: unbound},
			window:       0x40,
			uiIdentified: 0x50,
			want:         false,
			wantEvidence: unbound,
		},
		{
			name:         "no UI-identified window means no admission",
			selection:    xcodeWindowSelection{Evidence: unbound},
			window:       0x40,
			uiIdentified: 0,
			want:         false,
			wantEvidence: unbound,
		},
		{
			name:         "a zero window is never admitted",
			selection:    xcodeWindowSelection{Evidence: unbound},
			window:       0,
			uiIdentified: 0,
			want:         false,
			wantEvidence: unbound,
		},
		{
			name:         "an already bound selection keeps its own evidence",
			selection:    xcodeWindowSelection{Bound: true, Evidence: "AXDocument exactly matches the requested trace"},
			window:       0x40,
			uiIdentified: 0x40,
			want:         true,
			wantEvidence: "AXDocument exactly matches the requested trace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := admitUIIdentifiedWindow(tt.selection, tt.window, tt.uiIdentified)
			if got.Bound != tt.want {
				t.Errorf("Bound = %v, want %v", got.Bound, tt.want)
			}
			if got.Evidence != tt.wantEvidence {
				t.Errorf("Evidence = %q, want %q", got.Evidence, tt.wantEvidence)
			}
		})
	}
}
