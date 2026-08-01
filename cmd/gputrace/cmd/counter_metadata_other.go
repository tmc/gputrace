//go:build !darwin

package cmd

// applyXcodeCounterMetadata leaves tracks unannotated off darwin.
//
// The counter dictionary lives inside an installed Xcode and is read with
// plutil, neither of which exists here. Tracks keep the name and unit their
// own source gave them, which is what the enrichment falls back to on darwin
// when no Xcode is installed.
func applyXcodeCounterMetadata(tracks []CounterTrack) []CounterTrack { return tracks }
