//go:build darwin

package xcodebindings

import (
	"testing"

	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

func TestGTMioCounterDataElementEncodings(t *testing.T) {
	_ = gtshaderprofiler.GetGTMioCounterDataClass()
	cls := objc.GetClass("GTMioCounterData")
	if cls == 0 {
		t.Fatal("GTMioCounterData class is unavailable")
	}
	for _, test := range []struct {
		selector string
		want     string
	}{
		{"values", "^d16@0:8"},
		{"timestamps", "^Q16@0:8"},
		{"sampleCount", "Q16@0:8"},
	} {
		method := objectivec.Class_getInstanceMethod(cls, objectivec.SEL(objc.Sel(test.selector)))
		if method == 0 {
			t.Fatalf("%s method is unavailable", test.selector)
		}
		if got := objc.GoString(objectivec.Method_getTypeEncoding(method)); got != test.want {
			t.Fatalf("%s encoding = %q, want %q", test.selector, got, test.want)
		}
	}
}
