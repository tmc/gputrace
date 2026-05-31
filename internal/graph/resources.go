package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/gputrace/internal/trace"
)

type resourceAccess struct {
	EncoderIndex int
	EncoderLabel string
	Address      uint64
	Name         string
	Usage        string
}

type resourceSummary struct {
	Address uint64
	Name    string
	Uses    int
}

func collectResourceAccesses(t *trace.Trace) ([]resourceAccess, []resourceSummary, error) {
	events, err := t.ParseDependencyEvents()
	if err != nil {
		return nil, nil, fmt.Errorf("parse dependency events: %w", err)
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Offset < events[j].Offset
	})

	var accesses []resourceAccess
	resources := make(map[uint64]*resourceSummary)
	currentEncoder := -1
	currentLabel := ""

	for _, ev := range events {
		switch ev.Type {
		case trace.EventCS:
			currentEncoder++
			currentLabel = ev.Label
			if currentLabel == "" {
				currentLabel = fmt.Sprintf("Encoder %d", currentEncoder)
			}
		case trace.EventBind, trace.EventUse:
			if currentEncoder < 0 || ev.Address == 0 {
				continue
			}

			name := ev.Name
			if name == "" {
				name = fmt.Sprintf("buf_0x%x", ev.Address)
			}
			usage := resourceUsageLabel(ev.Usage)
			if ev.Type == trace.EventUse {
				usage = "Read"
			}
			usage = strings.ReplaceAll(usage, "|", "")

			accesses = append(accesses, resourceAccess{
				EncoderIndex: currentEncoder,
				EncoderLabel: currentLabel,
				Address:      ev.Address,
				Name:         name,
				Usage:        usage,
			})

			summary := resources[ev.Address]
			if summary == nil {
				summary = &resourceSummary{Address: ev.Address, Name: name}
				resources[ev.Address] = summary
			}
			if summary.Name == "" || strings.HasPrefix(summary.Name, "buf_0x") {
				summary.Name = name
			}
			summary.Uses++
		}
	}

	summaries := make([]resourceSummary, 0, len(resources))
	for _, summary := range resources {
		summaries = append(summaries, *summary)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Address < summaries[j].Address
	})

	return accesses, summaries, nil
}

func dotLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func resourceNodeID(address uint64) string {
	return fmt.Sprintf("res_%x", address)
}

func resourceUsageLabel(usage trace.MTLResourceUsage) string {
	switch {
	case usage.IsReadWrite():
		return "ReadWrite"
	case usage.IsRead():
		return "Read"
	case usage.IsWrite():
		return "Write"
	default:
		return usage.String()
	}
}
