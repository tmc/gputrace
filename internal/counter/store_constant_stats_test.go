package counter

import "testing"

func TestStoreConstantCalculationStats(t *testing.T) {
	for _, name := range []string{
		"low-occupancy-high-registers",
		"high-occupancy-low-registers",
		"high-alu-complex-math",
		"low-alu-simple-add",
		"06-six-encoders",
	} {
		t.Run(name, func(t *testing.T) {
			stats, err := ExtractStoreStats(openFixture(t, name), 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, ps := range stats.Pipelines {
				if ps.ConstantCalculationTemporaryRegisterCount != 1 {
					t.Errorf("%s constant calculation temporary registers = %d, want 1", ps.FunctionName, ps.ConstantCalculationTemporaryRegisterCount)
				}
				if !ps.ConstantCalculationPhasePresent {
					t.Errorf("%s constant calculation phase present = false, want true", ps.FunctionName)
				}
			}
		})
	}
}
