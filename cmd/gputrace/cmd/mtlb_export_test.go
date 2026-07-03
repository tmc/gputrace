package cmd

import (
	"path/filepath"
	"testing"
)

func TestValidateMTLBExportFormat(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr string
	}{
		{name: "json", format: "json"},
		{name: "csv", format: "csv"},
		{
			name:    "empty",
			wantErr: `invalid mtlb export format "" (must be json or csv)`,
		},
		{
			name:    "uppercase",
			format:  "JSON",
			wantErr: `invalid mtlb export format "JSON" (must be json or csv)`,
		},
		{
			name:    "yaml",
			format:  "yaml",
			wantErr: `invalid mtlb export format "yaml" (must be json or csv)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateMTLBExportFormat(tt.format)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("validateMTLBExportFormat(%q) returned nil error, want %q", tt.format, tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateMTLBExportFormat(%q): %v", tt.format, err)
			}
			if got != tt.format {
				t.Fatalf("validateMTLBExportFormat(%q) = %q, want %q", tt.format, got, tt.format)
			}
		})
	}
}

func TestMTLBExportFunctionsValidatesFormatBeforeTraceIO(t *testing.T) {
	missingTrace := filepath.Join(t.TempDir(), "missing.gputrace")
	err := runMTLBExportFunctions(mtlbExportFunctionsCmd, []string{missingTrace}, &mtlbExportOptions{
		format: "yaml",
	})
	if err == nil {
		t.Fatal("export-functions succeeded, want error")
	}

	want := `invalid mtlb export format "yaml" (must be json or csv)`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
