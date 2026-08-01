//go:build darwin

package xcodebindings

import (
	"slices"
	"testing"

	"github.com/tmc/apple/objc"
)

func nsNumber(t *testing.T, v int32) objc.ID {
	t.Helper()
	id := objc.Send[objc.ID](objc.ID(uintptr(objc.GetClass("NSNumber"))), objc.Sel("numberWithInt:"), v)
	if id == 0 {
		t.Fatal("NSNumber numberWithInt: returned nil")
	}
	return id
}

func nsDictionary(t *testing.T, key objc.ID) objc.ID {
	t.Helper()
	id := objc.Send[objc.ID](objc.ID(uintptr(objc.GetClass("NSDictionary"))),
		objc.Sel("dictionaryWithObject:forKey:"), objc.String("value"), key)
	if id == 0 {
		t.Fatal("NSDictionary dictionaryWithObject:forKey: returned nil")
	}
	return id
}

// TestDictionaryKeys covers both key classes streamData archives use. An
// NSNumber key crashed the process before dictionaryKeys checked for
// UTF8String; pipelinePerformanceStatistics is keyed that way.
func TestDictionaryKeys(t *testing.T) {
	tests := []struct {
		name string
		key  func(*testing.T) objc.ID
		want []string
	}{
		{
			name: "string key",
			key:  func(*testing.T) objc.ID { return objc.String("Compile Performance") },
			want: []string{"Compile Performance"},
		},
		{
			name: "number key",
			key:  func(t *testing.T) objc.ID { return nsNumber(t, 451) },
			want: []string{"451"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dictionaryKeys(nsDictionary(t, tt.key(t)), 4)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDictionaryKeysNil(t *testing.T) {
	if got := dictionaryKeys(0, 4); got != nil {
		t.Errorf("got %q, want nil", got)
	}
}
