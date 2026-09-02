//go:build darwin

package xcodebindings

import "testing"

func TestShaderCostRows(t *testing.T) {
	summary := ProcessedStreamData{
		GPUTime: 1000,
		Pipelines: []PipelineRecord{
			{FunctionName: "named", FunctionObjectID: 1485, LibraryObjectID: 1432, ComputeTime: 250},
			{FunctionObjectID: 1483, LibraryObjectID: 1432, ComputeTime: 100},
			{FunctionObjectID: 1484, LibraryObjectID: 1432, ComputeTime: 200},
			{FunctionObjectID: 1486, LibraryObjectID: 1432, ComputeTime: 50},
		},
	}
	rows, total, err := shaderCostRows(summary)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1000 {
		t.Fatalf("total = %d, want 1000", total)
	}
	wantNames := []string{"named", "MTLFunction 52", "MTLFunction 51", "MTLFunction 54"}
	wantCosts := []float64{25, 20, 10, 5}
	for i := range wantNames {
		if rows[i].Name != wantNames[i] || rows[i].Cost != wantCosts[i] {
			t.Errorf("row %d = {%q, %g}, want {%q, %g}", i, rows[i].Name, rows[i].Cost, wantNames[i], wantCosts[i])
		}
	}
}

func TestShaderCostRowsZeroGPUTime(t *testing.T) {
	if _, _, err := shaderCostRows(ProcessedStreamData{}); err == nil {
		t.Fatal("shaderCostRows succeeded with zero GPU time")
	}
}
