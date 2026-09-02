package graph

import (
	"fmt"
	"io"
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
func (g *DOTGenerator) Generate(w io.Writer, t *trace.Trace, config *Config) error {
	var (
		output string
		err    error
	)
	switch config.Type {
	case "hierarchy":
		output, err = g.generateHierarchy(t, config)
	case "flow":
		output, err = g.generateFlow(t, config)
	case "resources":
		output, err = g.generateResources(t, config)
	default:
		return fmt.Errorf("unsupported graph type: %s", config.Type)
	}
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, output)
	return err
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
	sb.WriteString("  attribution [label=\"Warning: command-buffer ownership of CS labels is heuristic\", shape=note, color=orange];\n")
	sb.WriteString("  trace -> attribution [style=dashed, color=orange];\n\n")

	// Parse command buffers
	commandBuffers, err := t.ParseCommandBuffers()
	if err != nil {
		return "", fmt.Errorf("parse command buffers: %w", err)
	}

	// Parse encoders
	encoders := t.ParseComputeEncoders()

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
			sb.WriteString(fmt.Sprintf("  %s -> %s [style=dashed, label=\"heuristic\"];\n", cbID, encID))
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

// generateFlow shows CS labels in their observed order. The trace parser does
// not currently prove command-buffer ownership or dispatch membership here.
func (g *DOTGenerator) generateFlow(t *trace.Trace, config *Config) (string, error) {
	var sb strings.Builder

	// Header - vertical flow (top to bottom)
	sb.WriteString("digraph GPUTrace {\n")
	sb.WriteString("  rankdir=TB;\n")
	sb.WriteString("  node [shape=box, style=rounded];\n\n")

	// Parse observed CS labels.
	encoders := t.ParseComputeEncoders()

	sb.WriteString("  note [label=\"Observed CS-label order only\\nCommand-buffer and dispatch edges unavailable\", shape=note, color=orange];\n\n")

	sb.WriteString("  // CS labels in observed order\n")
	for i, encoder := range encoders {
		encID := fmt.Sprintf("label%d", i)

		label := encoder.Label
		if label == "" {
			label = fmt.Sprintf("CS label %d", i)
		}
		sb.WriteString(fmt.Sprintf("  %s [label=\"%s\", style=\"rounded,filled\", fillcolor=\"#CC5555\", fontcolor=white, width=2];\n", encID, dotLabel(label)))
	}

	if len(encoders) > 0 {
		sb.WriteString("  note -> label0 [style=dashed, label=\"observed order\"];\n")
	}
	for i := 0; i < len(encoders)-1; i++ {
		sb.WriteString(fmt.Sprintf("  label%d -> label%d [style=dashed, label=\"observed order\"];\n", i, i+1))
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
