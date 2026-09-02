package tracebundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectPayload(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  Payload
	}{
		{
			name: "full",
			files: map[string]string{
				"capture":                          "MTSP capture",
				"device-resources-0x1234000":       "MTSP resources",
				"trace.gpuprofiler_raw/streamData": "profile",
			},
			want: Payload{Class: PayloadFull, HasCapture: true, HasRawResources: true, HasProfilerStream: true},
		},
		{
			name: "profiler only",
			files: map[string]string{
				"index":                            "index",
				"metadata":                         "metadata",
				"store":                            "store",
				"trace.gpuprofiler_raw/streamData": "profile",
				"thumbnails/thumbnail_0_0@2x.png":  "png",
			},
			want: Payload{Class: PayloadProfilerOnly, HasProfilerStream: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range test.files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := InspectPayload(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("InspectPayload() = %+v, want %+v", got, test.want)
			}
		})
	}
}
