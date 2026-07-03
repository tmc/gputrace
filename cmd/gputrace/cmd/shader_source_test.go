package cmd

import (
	"path/filepath"
	"testing"
)

func TestValidateShaderSourceFormatAcceptsKnownValues(t *testing.T) {
	for _, format := range []string{"text", "html", "json"} {
		t.Run(format, func(t *testing.T) {
			got, err := validateShaderSourceFormat(format)
			if err != nil {
				t.Fatalf("validateShaderSourceFormat(%q): %v", format, err)
			}
			if got != format {
				t.Fatalf("format = %q, want %q", got, format)
			}
		})
	}
}

func TestValidateShaderSourceFormatRejectsUnknownValues(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "empty",
			format: "",
			want:   `invalid shader-source format "" (must be text, html, or json)`,
		},
		{
			name:   "xml",
			format: "xml",
			want:   `invalid shader-source format "xml" (must be text, html, or json)`,
		},
		{
			name:   "uppercase",
			format: "HTML",
			want:   `invalid shader-source format "HTML" (must be text, html, or json)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateShaderSourceFormat(tt.format)
			if err == nil {
				t.Fatal("validateShaderSourceFormat succeeded, want error")
			}
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestRunShaderSourceValidatesFormatBeforeTraceIO(t *testing.T) {
	missingTrace := filepath.Join(t.TempDir(), "missing.gputrace")
	err := runShaderSource(nil, []string{missingTrace, "kernel"}, &shaderSourceOptions{
		format: "xml",
	})
	if err == nil {
		t.Fatal("runShaderSource succeeded, want error")
	}
	want := `invalid shader-source format "xml" (must be text, html, or json)`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
