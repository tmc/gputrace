package environment

import "testing"

func TestCompareProjection(t *testing.T) {
	available := func(value string) Value {
		return Value{Value: value, Source: "test", Parser: "v1", Availability: "available"}
	}
	base := Snapshot{
		Schema: SchemaV1,
		Exact: Exact{
			Workload: available("decode"), Device: available("M4"), Driver: available("xcode-17"),
			Runtime: available("mlx-1"), CaptureMode: available("replay"), TimingSource: available("APS"),
		},
		Capabilities: map[string]Value{"family-9": available("true")},
		Information:  map[string]Value{"observed_at": available("one")},
		Catalog:      Catalog{Revision: "v1", Digest: "sha256:catalog"},
	}
	other := base
	other.Information = map[string]Value{"observed_at": available("two")}
	result, err := Compare(base, other, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Label != "controlled" || len(result.InformationalChanges) != 1 {
		t.Fatalf("informational comparison = %+v", result)
	}

	other.Exact.Device = available("M5")
	result, err = Compare(base, other, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Label != "incompatible" {
		t.Fatalf("mismatch label = %q", result.Label)
	}
	result, err = Compare(base, other, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Label != "cross-environment, not causally attributable" {
		t.Fatalf("override label = %q", result.Label)
	}
}
