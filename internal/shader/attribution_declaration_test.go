package shader

import (
	"strings"
	"testing"
)

func TestAnalyzeShaderTextPlacesWholeCostAtDeclaration(t *testing.T) {
	source := "using namespace metal;\nkernel void k() {\n  expensive();\n}\n"
	lines, err := analyzeShaderText(source, 2, &ShaderMetrics{TotalDurationNs: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d attributed lines, want 1", len(lines))
	}
	line := lines[0]
	if line.LineNumber != 2 || line.SourceCode != "kernel void k() {" || line.GPUTimePercent != 100 || line.InstructionType != "declaration" {
		t.Fatalf("declaration = %+v", line)
	}
	if line.EstimatedCost != 0 || line.ALUUtilization != 0 || line.MemoryBandwidth != 0 || len(line.Hints) != 0 {
		t.Fatalf("declaration contains estimated line metrics: %+v", line)
	}
}

func TestDeclarationFormattingDisclosesGranularity(t *testing.T) {
	attr := &ShaderSourceAttribution{
		ShaderName:             "k",
		SourceFile:             "k.metal",
		AttributionLevel:       "declaration",
		AttributionGranularity: "kernel_total_at_declaration",
		Lines: []SourceLineAttribution{{
			LineNumber:      2,
			SourceCode:      "kernel void k() {}",
			GPUTimePercent:  100,
			InstructionType: "declaration",
		}},
	}
	text := FormatShaderSourceAttribution(attr, false)
	if !strings.Contains(text, "Attribution: declaration (kernel_total_at_declaration)") || !strings.Contains(text, "kernel void k() {}") {
		t.Fatalf("text output does not disclose declaration granularity:\n%s", text)
	}
	html := FormatShaderSourceAttributionHTML(attr)
	if !strings.Contains(html, "Attribution:</strong> declaration (kernel_total_at_declaration)") || !strings.Contains(html, "kernel void k() {}") {
		t.Fatalf("HTML output does not disclose declaration granularity:\n%s", html)
	}
}
