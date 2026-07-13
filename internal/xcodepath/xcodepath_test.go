package xcodepath

import (
	"errors"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name      string
		app       string
		developer string
		err       error
		want      string
	}{
		{"environment", "/Volumes/Tools/Xcode-beta.app", "/Applications/Xcode.app/Contents/Developer", nil, "/Volumes/Tools/Xcode-beta.app/" + frameworkSuffix},
		{"selected", "", "/Applications/Xcode-beta.app/Contents/Developer\n", nil, "/Applications/Xcode-beta.app/" + frameworkSuffix},
		{"fallback", "", "", errors.New("not selected"), "/Applications/Xcode.app/" + frameworkSuffix},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(string) string { return tt.app }
			developerDir := func() (string, error) { return tt.developer, tt.err }
			if got := resolve(getenv, developerDir); got != tt.want {
				t.Fatalf("resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}
