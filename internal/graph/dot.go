package graph

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/tmc/gputrace/internal/trace"
)

// DOTGenerator generates Graphviz DOT format output.
type DOTGenerator struct{}

// NewDOTGenerator creates a new DOT generator.
func NewDOTGenerator() *DOTGenerator {
	return &DOTGenerator{}
}

// Generate creates a DOT graph from the trace.
func (g *DOTGenerator) Generate(t *trace.Trace, config *Config) (string, error) {
	switch config.Type {
	case "hierarchy":
		return g.generateHierarchy(t, config)
	case "flow":
		return g.generateFlow(t, config)
	case "resources":
		return g.generateResources(t, config)
	default:
		return "", fmt.Errorf("unsupported graph type: %s", config.Type)
	}
}

// generateHierarchy creates a hierarchical graph: command buffers → encoders → shaders.
func (g *DOTGenerator) generateHierarchy(t *trace.Trace, config *Config) (string, error) {
	var sb strings.Builder

	// Header
	sb.WriteString("digraph GPUTrace {\n")
	sb.WriteString("  rankdir=LR;\n")
	sb.WriteString("  node [shape=box, style=rounded];\n\n")

	// Root node
	sb.WriteString("  trace [label=\"GPU Trace\", shape=ellipse, style=filled, fillcolor=lightblue];\n\n")

	// Parse command buffers
	commandBuffers, err := t.ParseCommandBuffers()
	if err != nil {
		return "", fmt.Errorf("parse command buffers: %w", err)
	}

	// Parse encoders
	encoders, err := t.ParseComputeEncoders()
	if err != nil {
		return "", fmt.Errorf("parse encoders: %w", err)
	}

	// Get shader metrics if timing is requested
	var shaderMetrics map[string]*ShaderInfo
	if config.ShowTiming {
		var err error
		shaderMetrics, err = getShaderMetrics(t)
		if err != nil {
			return "", fmt.Errorf("extract shader metrics: %w", err)
		}
	}

	// Add command buffers
	sb.WriteString("  // Command Buffers\n")
	for _, cb := range commandBuffers {
		cbID := fmt.Sprintf("cb%d", cb.Index)
		label := fmt.Sprintf("Command Buffer %d", cb.Index)
		if config.ShowTiming {
			label += fmt.Sprintf("\\nTimestamp: %d", cb.Timestamp)
		}
		sb.WriteString(fmt.Sprintf("  %s [label=\"%s\", style=filled, fillcolor=lightgreen];\n", cbID, dotLabel(label)))
		sb.WriteString(fmt.Sprintf("  trace -> %s;\n", cbID))
	}
	sb.WriteString("\n")

	// Add encoders
	sb.WriteString("  // Encoders\n")
	encodersByCommandBuffer := g.groupEncodersByCommandBuffer(t, encoders)

	for cbIndex, cbEncoders := range encodersByCommandBuffer {
		cbID := fmt.Sprintf("cb%d", cbIndex)
		for _, encoder := range cbEncoders {
			encID := fmt.Sprintf("enc%d", encoder.Index)
			label := encoder.Label
			if label == "" {
				label = fmt.Sprintf("Encoder %d", encoder.Index)
			}
			if config.ShowTiming && shaderMetrics != nil {
				if metrics, ok := shaderMetrics[encoder.Label]; ok {
					label += fmt.Sprintf("\\nDuration: %.2fms", float64(metrics.Duration)/1e6)
				}
			}
			sb.WriteString(fmt.Sprintf("  %s [label=\"%s\", style=filled, fillcolor=lightyellow];\n", encID, dotLabel(label)))
			sb.WriteString(fmt.Sprintf("  %s -> %s;\n", cbID, encID))
		}
	}
	sb.WriteString("\n")

	// Add shaders (from encoder labels)
	sb.WriteString("  // Shaders\n")
	shaderNodes := make(map[string]bool)
	for _, encoder := range encoders {
		if encoder.Label != "" {
			// Extract shader name from encoder label (e.g., "Encoder_1_simple_add" -> "simple_add")
			shaderName := extractShaderName(encoder.Label)
			if shaderName != "" && !shaderNodes[shaderName] {
				shaderID := fmt.Sprintf("shader_%s", sanitizeID(shaderName))
				label := shaderName
				if config.ShowTiming && shaderMetrics != nil {
					if metrics, ok := shaderMetrics[shaderName]; ok {
						label += fmt.Sprintf("\\nExec: %d times", metrics.ExecutionCount)
						label += fmt.Sprintf("\\nAvg: %.2fms", float64(metrics.Duration)/float64(metrics.ExecutionCount)/1e6)
					}
				}
				sb.WriteString(fmt.Sprintf("  %s [label=\"%s\", shape=hexagon, style=filled, fillcolor=lightcoral];\n", shaderID, dotLabel(label)))
				shaderNodes[shaderName] = true
			}
		}
	}
	sb.WriteString("\n")

	// Add edges from encoders to shaders
	sb.WriteString("  // Encoder -> Shader connections\n")
	for _, encoder := range encoders {
		if encoder.Label != "" {
			shaderName := extractShaderName(encoder.Label)
			if shaderName != "" {
				encID := fmt.Sprintf("enc%d", encoder.Index)
				shaderID := fmt.Sprintf("shader_%s", sanitizeID(shaderName))
				sb.WriteString(fmt.Sprintf("  %s -> %s;\n", encID, shaderID))
			}
		}
	}

	sb.WriteString("}\n")

	return sb.String(), nil
}

// generateFlow creates a temporal execution flow graph matching Xcode Instruments style.
func (g *DOTGenerator) generateFlow(t *trace.Trace, config *Config) (string, error) {
	var sb strings.Builder

	// Header - vertical flow (top to bottom)
	sb.WriteString("digraph GPUTrace {\n")
	sb.WriteString("  rankdir=TB;\n")
	sb.WriteString("  node [shape=box, style=rounded];\n\n")

	// Parse command buffers
	commandBuffers, err := t.ParseCommandBuffers()
	if err != nil {
		return "", fmt.Errorf("parse command buffers: %w", err)
	}

	// Parse encoders
	encoders, err := t.ParseComputeEncoders()
	if err != nil {
		return "", fmt.Errorf("parse encoders: %w", err)
	}

	// Add command buffer at top
	if len(commandBuffers) > 0 {
		cbID := "cb0"
		label := "MultipleEncoders_6" // Or use cb label if available
		sb.WriteString(fmt.Sprintf("  %s [label=\"%s\", shape=box, style=filled, fillcolor=\"#2B2B2B\", fontcolor=white, width=2];\n\n", cbID, label))
	}

	// Add encoders in vertical flow
	sb.WriteString("  // Encoders in execution order\n")
	for i, encoder := range encoders {
		encID := fmt.Sprintf("enc%d", i)

		// Encoder node
		label := encoder.Label
		if label == "" {
			label = fmt.Sprintf("Encoder %d", i)
		}

		// Red rounded box for encoder
		sb.WriteString(fmt.Sprintf("  %s [label=\"%s\", style=\"rounded,filled\", fillcolor=\"#CC5555\", fontcolor=white, width=2];\n", encID, dotLabel(label)))

		// Add dispatch nodes (blue grids) below each encoder
		// Assuming 3 dispatches per encoder (as shown in Xcode screenshot)
		dispatchCount := 3
		sb.WriteString("  // Dispatches for encoder\n")

		// Create invisible rank for dispatch nodes
		sb.WriteString("  { rank=same; ")
		for d := 0; d < dispatchCount; d++ {
			dispID := fmt.Sprintf("%s_d%d", encID, d)
			sb.WriteString(dispID)
			if d < dispatchCount-1 {
				sb.WriteString("; ")
			}
		}
		sb.WriteString(" }\n")

		// Define dispatch nodes
		for d := 0; d < dispatchCount; d++ {
			dispID := fmt.Sprintf("%s_d%d", encID, d)
			sb.WriteString(fmt.Sprintf("  %s [label=\"\", shape=square, style=filled, fillcolor=\"#4488CC\", width=0.3, height=0.3, fixedsize=true];\n", dispID))
		}

		// Connect encoder to its dispatches
		for d := 0; d < dispatchCount; d++ {
			dispID := fmt.Sprintf("%s_d%d", encID, d)
			sb.WriteString(fmt.Sprintf("  %s -> %s [arrowhead=none, color=\"#666666\"];\n", encID, dispID))
		}

		sb.WriteString("\n")
	}

	// Add flow connections between encoders
	sb.WriteString("  // Execution flow\n")
	if len(commandBuffers) > 0 && len(encoders) > 0 {
		// Connect command buffer to first encoder
		sb.WriteString(fmt.Sprintf("  cb0 -> enc0 [color=\"#666666\"];\n"))
	}

	for i := 0; i < len(encoders)-1; i++ {
		// Connect from last dispatch of current encoder to next encoder
		currEncID := fmt.Sprintf("enc%d", i)
		nextEncID := fmt.Sprintf("enc%d", i+1)
		lastDispID := fmt.Sprintf("%s_d1", currEncID) // Middle dispatch for visual clarity

		sb.WriteString(fmt.Sprintf("  %s -> %s [color=\"#666666\"];\n", lastDispID, nextEncID))
	}

	sb.WriteString("}\n")
	return sb.String(), nil
}

// generateResources creates a resource usage graph.
func (g *DOTGenerator) generateResources(t *trace.Trace, config *Config) (string, error) {
	accesses, resources, err := collectResourceAccesses(t)
	if err != nil {
		return "", err
	}
	if len(accesses) == 0 {
		return "", fmt.Errorf("no resource usage events found")
	}

	var sb strings.Builder
	sb.WriteString("digraph GPUTraceResources {\n")
	sb.WriteString("  rankdir=LR;\n")
	sb.WriteString("  node [shape=box, style=rounded];\n\n")

	encoderSeen := make(map[int]bool)
	sb.WriteString("  // Encoders\n")
	for _, access := range accesses {
		if encoderSeen[access.EncoderIndex] {
			continue
		}
		encoderSeen[access.EncoderIndex] = true
		label := access.EncoderLabel
		if label == "" {
			label = fmt.Sprintf("Encoder %d", access.EncoderIndex)
		}
		sb.WriteString(fmt.Sprintf("  enc%d [label=\"%s\", style=filled, fillcolor=lightyellow];\n",
			access.EncoderIndex, dotLabel(label)))
	}
	sb.WriteString("\n")

	sb.WriteString("  // Resources\n")
	for _, resource := range resources {
		label := fmt.Sprintf("%s\\n0x%x", resource.Name, resource.Address)
		if config.ShowMemory {
			label += fmt.Sprintf("\\n%d accesses", resource.Uses)
		}
		sb.WriteString(fmt.Sprintf("  %s [label=\"%s\", shape=cylinder, style=filled, fillcolor=lightblue];\n",
			resourceNodeID(resource.Address), dotLabel(label)))
	}
	sb.WriteString("\n")

	sb.WriteString("  // Resource usage\n")
	for _, access := range accesses {
		sb.WriteString(fmt.Sprintf("  enc%d -> %s [label=\"%s\"];\n",
			access.EncoderIndex, resourceNodeID(access.Address), dotLabel(access.Usage)))
	}

	sb.WriteString("}\n")
	return sb.String(), nil
}

// groupEncodersByCommandBuffer groups encoders by their command buffer index.
func (g *DOTGenerator) groupEncodersByCommandBuffer(t *trace.Trace, encoders []*trace.ComputeEncoder) map[int][]*trace.ComputeEncoder {
	result := make(map[int][]*trace.ComputeEncoder)

	// For now, assume encoders are in order and group them sequentially
	// In a real implementation, you'd parse the trace to determine which encoder
	// belongs to which command buffer

	commandBuffers, err := t.ParseCommandBuffers()
	if err != nil || len(commandBuffers) == 0 {
		// If we can't parse command buffers, put all encoders in CB 0
		result[0] = encoders
		return result
	}

	// Simple heuristic: distribute encoders evenly across command buffers
	encodersPerCB := len(encoders) / len(commandBuffers)
	if encodersPerCB == 0 {
		encodersPerCB = 1
	}

	for i, encoder := range encoders {
		cbIndex := i / encodersPerCB
		if cbIndex >= len(commandBuffers) {
			cbIndex = len(commandBuffers) - 1
		}
		result[cbIndex] = append(result[cbIndex], encoder)
	}

	return result
}

// sanitizeID sanitizes a string to be used as a DOT node ID.
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "node"
	}
	return b.String()
}
