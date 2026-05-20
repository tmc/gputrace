package shader

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tmc/gputrace/internal/trace"
)

// ShaderSourceAttribution represents performance attribution for a shader's source code.
type ShaderSourceAttribution struct {
	// Shader identification
	ShaderName string
	SourceFile string

	// Overall shader metrics
	Metrics *ShaderMetrics

	// Per-line attribution
	Lines []SourceLineAttribution

	// Hot spots (lines with highest cost)
	HotSpots []SourceLineAttribution
}

// SourceLineAttribution represents performance metrics attributed to a single source line.
type SourceLineAttribution struct {
	LineNumber int
	SourceCode string

	// Performance metrics (estimated distribution of shader metrics to this line)
	GPUTimePercent  float64 // Percentage of this shader's GPU time attributed to this line
	ALUUtilization  float64 // Estimated ALU utilization for this line (0-100%)
	MemoryBandwidth float64 // Estimated memory bandwidth for this line (GB/s)
	EstimatedCost   float64 // Relative cost estimate (0-100)

	// Instruction classification
	InstructionType string // "compute", "memory", "control", "other"
	Complexity      int    // Estimated instruction complexity (1-10)

	// Optimization hints
	IsHotSpot bool
	Hints     []string
}

// ExtractShaderSourceAttribution extracts source-level performance attribution for a specific shader.
func ExtractShaderSourceAttribution(t *trace.Trace, shaderName string) (*ShaderSourceAttribution, error) {
	mapper := NewShaderSourceMapper()
	if err := mapper.IndexShaderSources(); err != nil {
		return nil, fmt.Errorf("index shader sources: %w", err)
	}
	if t != nil {
		if err := mapper.IndexTraceBundleSources(t.Path); err != nil {
			return nil, fmt.Errorf("index trace shader sources: %w", err)
		}
	}
	return ExtractShaderSourceAttributionWithMapper(t, shaderName, mapper)
}

// ExtractShaderSourceAttributionWithMapper extracts source-level performance
// attribution using a caller-provided source mapper.
func ExtractShaderSourceAttributionWithMapper(t *trace.Trace, shaderName string, mapper *ShaderSourceMapper) (*ShaderSourceAttribution, error) {
	// Get shader metrics
	metricsReport, err := ExtractShaderMetrics(t)
	if err != nil {
		return nil, fmt.Errorf("extract shader metrics: %w", err)
	}

	// Find metrics for the requested shader
	var shaderMetrics *ShaderMetrics
	for _, sm := range metricsReport.Shaders {
		if sm.Name == shaderName || strings.Contains(sm.Name, shaderName) {
			shaderMetrics = sm
			break
		}
	}

	if shaderMetrics == nil {
		return nil, fmt.Errorf("shader %q not found in trace", shaderName)
	}

	if mapper == nil {
		return nil, fmt.Errorf("no shader source mapper")
	}

	sourceFile, startLine := mapper.GetSourceLocation(shaderName)
	if sourceFile == "" {
		return nil, fmt.Errorf("source file for shader %q not found", shaderName)
	}

	// Read and analyze source file
	lines, err := analyzeShaderSource(sourceFile, startLine, shaderMetrics)
	if err != nil {
		return nil, fmt.Errorf("analyze source: %w", err)
	}

	// Identify hot spots (top 20% by cost)
	hotSpots := identifyHotSpots(lines, 0.2)

	attribution := &ShaderSourceAttribution{
		ShaderName: shaderName,
		SourceFile: sourceFile,
		Metrics:    shaderMetrics,
		Lines:      lines,
		HotSpots:   hotSpots,
	}

	return attribution, nil
}

// analyzeShaderSource reads the shader source and attributes performance to each line.
func analyzeShaderSource(sourceFile string, startLine int, metrics *ShaderMetrics) ([]SourceLineAttribution, error) {
	f, err := os.Open(sourceFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []SourceLineAttribution
	scanner := bufio.NewScanner(f)
	lineNum := 0
	inFunction := false
	braceDepth := 0
	instructionCount := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Track when we enter the kernel function
		if lineNum == startLine {
			inFunction = true
		}

		// Track braces to know when we exit the function
		if inFunction {
			braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if braceDepth < 0 {
				inFunction = false
			}
		}

		// Only analyze lines within the kernel function
		if !inFunction {
			continue
		}

		// Analyze the line
		attr := SourceLineAttribution{
			LineNumber: lineNum,
			SourceCode: line,
		}

		// Classify instruction type and estimate cost
		attr.InstructionType, attr.Complexity = classifyInstruction(trimmed)

		// Estimate relative cost (this is a heuristic)
		attr.EstimatedCost = estimateLineCost(trimmed, attr.InstructionType, attr.Complexity)

		// Count as instruction if not empty or comment
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			instructionCount++
		}

		lines = append(lines, attr)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Distribute shader metrics across lines based on estimated cost
	distributeMetrics(lines, metrics, instructionCount)

	// Add optimization hints
	addOptimizationHints(lines)

	return lines, nil
}

// classifyInstruction classifies the type and complexity of a Metal shader instruction.
func classifyInstruction(line string) (instrType string, complexity int) {
	// Empty or comment
	if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") {
		return "other", 0
	}

	// Memory operations
	if strings.Contains(line, "texture.") ||
		strings.Contains(line, ".sample(") ||
		strings.Contains(line, ".read(") ||
		strings.Contains(line, ".write(") ||
		strings.Contains(line, "device") && (strings.Contains(line, "[") || strings.Contains(line, "*")) {
		complexity := 3
		if strings.Contains(line, "texture") {
			complexity = 5 // Texture operations are more expensive
		}
		return "memory", complexity
	}

	// Control flow
	if strings.Contains(line, "if ") ||
		strings.Contains(line, "for ") ||
		strings.Contains(line, "while ") ||
		strings.Contains(line, "return") {
		return "control", 2
	}

	// Compute operations
	if strings.Contains(line, "*") ||
		strings.Contains(line, "+") ||
		strings.Contains(line, "-") ||
		strings.Contains(line, "/") ||
		strings.Contains(line, "sqrt") ||
		strings.Contains(line, "exp") ||
		strings.Contains(line, "log") ||
		strings.Contains(line, "sin") ||
		strings.Contains(line, "cos") {

		complexity := 2
		// Math functions are more expensive
		if strings.Contains(line, "sqrt") ||
			strings.Contains(line, "exp") ||
			strings.Contains(line, "log") {
			complexity = 4
		}
		if strings.Contains(line, "sin") || strings.Contains(line, "cos") {
			complexity = 5
		}
		return "compute", complexity
	}

	// Default
	return "other", 1
}

// estimateLineCost estimates the relative cost of a source line.
func estimateLineCost(line string, instrType string, complexity int) float64 {
	if line == "" || strings.HasPrefix(line, "//") {
		return 0
	}

	// Base cost on instruction type and complexity
	baseCost := float64(complexity)

	// Weight by instruction type
	switch instrType {
	case "memory":
		baseCost *= 2.0 // Memory ops are expensive
	case "compute":
		baseCost *= 1.5 // Compute ops are moderate
	case "control":
		baseCost *= 1.0 // Control flow is cheap
	default:
		baseCost *= 0.5
	}

	return baseCost
}

// distributeMetrics distributes shader-level metrics across source lines based on estimated cost.
// When real instruction counts are available (from PipelineStats/streamData), they are used
// to weight the distribution more accurately.
func distributeMetrics(lines []SourceLineAttribution, metrics *ShaderMetrics, instrCount int) {
	// Calculate total cost using instruction type weights
	// If real instruction counts are available, use them to weight line types
	computeWeight := 1.0
	memoryWeight := 2.0
	branchWeight := 0.5

	// Use real instruction counts to adjust weights if available
	if metrics != nil && metrics.InstructionCount > 0 {
		totalInstr := float64(metrics.InstructionCount)
		if totalInstr > 0 {
			// ALU instructions are compute-heavy
			aluRatio := float64(metrics.ALUInstructionCount) / totalInstr
			// FP32/FP16 are compute operations
			fpRatio := float64(metrics.FP32InstructionCount+metrics.FP16InstructionCount) / totalInstr
			// Branch instructions have control flow overhead
			branchRatio := float64(metrics.BranchInstructionCount) / totalInstr

			// Adjust weights based on actual instruction mix
			if aluRatio+fpRatio > 0.5 {
				computeWeight = 2.0 // Compute-heavy shader
			}
			if branchRatio > 0.1 {
				branchWeight = 1.5 // Branch-heavy shader (divergence)
			}
		}
	}

	// Calculate total weighted cost
	totalCost := 0.0
	for i := range lines {
		switch lines[i].InstructionType {
		case "compute":
			lines[i].EstimatedCost *= computeWeight
		case "memory":
			lines[i].EstimatedCost *= memoryWeight
		case "control":
			lines[i].EstimatedCost *= branchWeight
		}
		totalCost += lines[i].EstimatedCost
	}

	if totalCost == 0 {
		return
	}

	// Distribute metrics proportionally
	for i := range lines {
		if lines[i].EstimatedCost == 0 {
			continue
		}

		costRatio := lines[i].EstimatedCost / totalCost

		// Distribute GPU time
		lines[i].GPUTimePercent = costRatio * 100.0

		// Use real ALU utilization if available
		if metrics != nil {
			if metrics.ALUUtilization > 0 {
				// Scale ALU util by the compute contribution of this line
				if lines[i].InstructionType == "compute" {
					lines[i].ALUUtilization = metrics.ALUUtilization * costRatio * 2 // Boost compute lines
				} else {
					lines[i].ALUUtilization = metrics.ALUUtilization * costRatio
				}
			} else {
				lines[i].ALUUtilization = costRatio * 100.0
			}

			// Distribute memory bandwidth for memory ops
			if lines[i].InstructionType == "memory" && metrics.EstimatedBandwidth > 0 {
				lines[i].MemoryBandwidth = metrics.EstimatedBandwidth * costRatio
			}
		}
	}
}

// addOptimizationHints adds optimization hints to source lines based on analysis.
func addOptimizationHints(lines []SourceLineAttribution) {
	for i := range lines {
		line := &lines[i]
		src := strings.TrimSpace(line.SourceCode)

		// Memory access hints
		if line.InstructionType == "memory" {
			if strings.Contains(src, "texture.") {
				line.Hints = append(line.Hints, "Consider texture cache optimization")
			}
			if strings.Contains(src, "[") && !strings.Contains(src, "threadgroup") {
				line.Hints = append(line.Hints, "Consider using threadgroup memory for repeated access")
			}
		}

		// Compute hints
		if line.InstructionType == "compute" {
			if strings.Contains(src, "/") {
				line.Hints = append(line.Hints, "Division is expensive; consider multiplication by reciprocal")
			}
			if strings.Contains(src, "sqrt") {
				line.Hints = append(line.Hints, "sqrt is expensive; consider approximation if precision allows")
			}
			if strings.Contains(src, "exp") || strings.Contains(src, "log") {
				line.Hints = append(line.Hints, "Transcendental functions are expensive; consider LUT or approximation")
			}
		}

		// Control flow hints
		if line.InstructionType == "control" {
			if strings.Contains(src, "if") {
				line.Hints = append(line.Hints, "Branch divergence may reduce GPU efficiency")
			}
		}
	}
}

// identifyHotSpots identifies the most expensive source lines.
func identifyHotSpots(lines []SourceLineAttribution, threshold float64) []SourceLineAttribution {
	// Sort by cost
	sorted := make([]SourceLineAttribution, len(lines))
	copy(sorted, lines)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].EstimatedCost > sorted[j].EstimatedCost
	})

	// Take top threshold%
	count := int(float64(len(sorted)) * threshold)
	if count == 0 && len(sorted) > 0 {
		count = 1
	}
	if count > len(sorted) {
		count = len(sorted)
	}

	hotSpots := sorted[:count]

	// Mark as hot spots
	for i := range hotSpots {
		hotSpots[i].IsHotSpot = true
	}

	// Sort by line number for output
	sort.Slice(hotSpots, func(i, j int) bool {
		return hotSpots[i].LineNumber < hotSpots[j].LineNumber
	})

	return hotSpots
}

// FormatShaderSourceAttribution generates a human-readable annotated source view.
func FormatShaderSourceAttribution(attr *ShaderSourceAttribution, showHints bool) string {
	output := fmt.Sprintf("=== Shader Source Attribution: %s ===\n\n", attr.ShaderName)
	output += fmt.Sprintf("Source: %s\n", attr.SourceFile)

	if attr.Metrics != nil {
		output += fmt.Sprintf("Total GPU Time: %.2f ms\n", float64(attr.Metrics.TotalDurationNs)/1e6)
		output += fmt.Sprintf("Invocations: %d\n", attr.Metrics.InvocationCount)
		if attr.Metrics.Occupancy > 0 {
			output += fmt.Sprintf("Occupancy: %.1f%%\n", attr.Metrics.Occupancy*100)
		}
	}
	output += "\n"

	// Show hot spots summary
	if len(attr.HotSpots) > 0 {
		output += "Hot Spots (top 20% by cost):\n"
		for _, hs := range attr.HotSpots {
			output += fmt.Sprintf("  Line %4d: %5.1f%% | %s\n",
				hs.LineNumber,
				hs.GPUTimePercent,
				strings.TrimSpace(hs.SourceCode))
		}
		output += "\n"
	}

	// Annotated source view
	output += "Annotated Source:\n"
	output += fmt.Sprintf("%-6s %8s %8s %4s | %s\n", "Line", "Time%", "ALU%", "Type", "Source")
	output += strings.Repeat("-", 100) + "\n"

	for _, line := range attr.Lines {
		// Skip lines with zero cost
		if line.EstimatedCost == 0 {
			continue
		}

		marker := " "
		if line.IsHotSpot {
			marker = ">"
		}

		output += fmt.Sprintf("%s%-5d %7.1f%% %7.1f%% %4s | %s\n",
			marker,
			line.LineNumber,
			line.GPUTimePercent,
			line.ALUUtilization,
			line.InstructionType[:1], // First letter: m, c, o
			strings.TrimSpace(line.SourceCode))

		// Show hints for hot spots
		if showHints && len(line.Hints) > 0 {
			for _, hint := range line.Hints {
				output += fmt.Sprintf("      ├─ 💡 %s\n", hint)
			}
		}
	}

	return output
}

// FormatShaderSourceAttributionHTML generates an HTML view with syntax highlighting and interactive elements.
func FormatShaderSourceAttributionHTML(attr *ShaderSourceAttribution) string {
	html := `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<title>Shader Source Attribution: ` + attr.ShaderName + `</title>
<style>
body {
	font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
	font-size: 12px;
	background: #1e1e1e;
	color: #d4d4d4;
	padding: 20px;
}
h1 {
	color: #569cd6;
	font-size: 18px;
}
.summary {
	background: #252526;
	padding: 15px;
	margin: 20px 0;
	border-left: 3px solid #569cd6;
}
.source-line {
	display: flex;
	padding: 2px 0;
	border-bottom: 1px solid #333;
}
.source-line:hover {
	background: #2a2d2e;
}
.source-line.hotspot {
	background: #3a1f1f;
	border-left: 3px solid #f48771;
}
.line-num {
	width: 60px;
	color: #858585;
	text-align: right;
	padding-right: 10px;
}
.time-percent {
	width: 80px;
	text-align: right;
	padding-right: 10px;
	color: #ce9178;
}
.alu-percent {
	width: 80px;
	text-align: right;
	padding-right: 10px;
	color: #4ec9b0;
}
.type {
	width: 60px;
	text-align: center;
	font-size: 10px;
	color: #9cdcfe;
}
.code {
	flex: 1;
	white-space: pre;
	color: #d4d4d4;
}
.hint {
	margin-left: 80px;
	color: #dcdcaa;
	font-size: 11px;
	padding: 2px 0 2px 20px;
}
.hotspot-summary {
	background: #2d1f1f;
	padding: 10px;
	margin: 10px 0;
	border-left: 3px solid #f48771;
}
</style>
</head>
<body>
`

	html += fmt.Sprintf("<h1>Shader Source Attribution: %s</h1>\n", attr.ShaderName)
	html += fmt.Sprintf("<div class='summary'>\n")
	html += fmt.Sprintf("<strong>Source:</strong> %s<br>\n", attr.SourceFile)

	if attr.Metrics != nil {
		html += fmt.Sprintf("<strong>Total GPU Time:</strong> %.2f ms<br>\n",
			float64(attr.Metrics.TotalDurationNs)/1e6)
		html += fmt.Sprintf("<strong>Invocations:</strong> %d<br>\n",
			attr.Metrics.InvocationCount)
		if attr.Metrics.Occupancy > 0 {
			html += fmt.Sprintf("<strong>Occupancy:</strong> %.1f%%<br>\n",
				attr.Metrics.Occupancy*100)
		}
	}
	html += "</div>\n"

	// Hot spots
	if len(attr.HotSpots) > 0 {
		html += "<div class='hotspot-summary'>\n"
		html += "<strong>Hot Spots (top 20% by cost):</strong><br>\n"
		for _, hs := range attr.HotSpots {
			html += fmt.Sprintf("Line %d: %.1f%% - %s<br>\n",
				hs.LineNumber,
				hs.GPUTimePercent,
				strings.TrimSpace(hs.SourceCode))
		}
		html += "</div>\n"
	}

	// Annotated source
	html += "<div class='source'>\n"
	for _, line := range attr.Lines {
		if line.EstimatedCost == 0 {
			continue
		}

		class := "source-line"
		if line.IsHotSpot {
			class += " hotspot"
		}

		html += fmt.Sprintf("<div class='%s'>\n", class)
		html += fmt.Sprintf("  <span class='line-num'>%d</span>\n", line.LineNumber)
		html += fmt.Sprintf("  <span class='time-percent'>%.1f%%</span>\n", line.GPUTimePercent)
		html += fmt.Sprintf("  <span class='alu-percent'>%.1f%%</span>\n", line.ALUUtilization)
		html += fmt.Sprintf("  <span class='type'>%s</span>\n", line.InstructionType)
		html += fmt.Sprintf("  <span class='code'>%s</span>\n",
			strings.ReplaceAll(strings.TrimSpace(line.SourceCode), "<", "&lt;"))
		html += "</div>\n"

		// Hints
		for _, hint := range line.Hints {
			html += fmt.Sprintf("<div class='hint'>💡 %s</div>\n", hint)
		}
	}
	html += "</div>\n"

	html += `
</body>
</html>
`

	return html
}
