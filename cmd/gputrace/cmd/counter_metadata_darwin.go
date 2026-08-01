//go:build darwin

package cmd

import "github.com/tmc/gputrace/internal/counter"

// applyXcodeCounterMetadata annotates tracks whose names exactly match Xcode's
// counter dictionary. The dictionary is vocabulary, not a way to name opaque
// raw-counter ids, so unmatched archive-derived tracks retain their own names
// and units.
//
// Reading the dictionary means running plutil, so it is darwin only. The
// enrichment is optional by construction: see the stub in
// counter_metadata_other.go.
func applyXcodeCounterMetadata(tracks []CounterTrack) []CounterTrack {
	graph, err := counter.LoadGPUCounterGraph()
	if err != nil || graph == nil {
		return tracks
	}
	return applyXcodeCounterMetadataFromGraph(tracks, graph)
}

// applyXcodeCounterMetadataFromGraph applies one already-loaded dictionary.
// It is separate from applyXcodeCounterMetadata so tests need not depend on an
// installed Xcode bundle.
func applyXcodeCounterMetadataFromGraph(tracks []CounterTrack, graph *counter.GPUCounterGraph) []CounterTrack {
	if graph == nil {
		return tracks
	}
	groups := make(map[string][]string)
	for _, group := range graph.TimelineGroups {
		for _, name := range group.Counters {
			groups[name] = append(groups[name], group.Name)
		}
	}
	for i := range tracks {
		track := &tracks[i]
		metadata, ok := graph.Counters[track.Name]
		if !ok {
			continue
		}
		if metadata.Unit != "" {
			track.Unit = metadata.Unit
		}
		if metadata.Description != "" {
			track.Description = metadata.Description
		}
		track.XcodeGroups = append([]string(nil), groups[track.Name]...)
		track.XcodeCatalogPath = graph.Path
	}
	return tracks
}
