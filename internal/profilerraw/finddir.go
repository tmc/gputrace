package profilerraw

import (
	"os"
	"path/filepath"
)

// Xcode writes profiler output to a .gpuprofiler_raw directory that reaches
// callers in three shapes:
//
//	trace.gputrace.gpuprofiler_raw   sibling of the bundle
//	trace.gputrace/x.gpuprofiler_raw inside the bundle
//	x.gpuprofiler_raw                the directory itself
//
// Callers used to open-code some subset of these, so different subcommands
// disagreed about whether a given bundle had profiler data at all.

// FindDir returns the .gpuprofiler_raw directory for path, or "" if there is
// none. path may be a trace bundle or the profiler directory itself.
func FindDir(path string) string {
	return findDir(path, func(string) bool { return true })
}

// FindDirWithStreamData is FindDir restricted to directories that contain a
// streamData file. Callers that parse pipeline, dispatch, or encoder metadata
// need it and should treat a directory without it as absent.
func FindDirWithStreamData(path string) string {
	return findDir(path, HasStreamData)
}

// HasStreamData reports whether dir holds a streamData file.
func HasStreamData(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "streamData"))
	return err == nil
}

func findDir(path string, accept func(string) bool) string {
	if path == "" {
		return ""
	}
	if isDir(path) && filepath.Ext(path) == ".gpuprofiler_raw" && accept(path) {
		return path
	}
	if adjacent := path + ".gpuprofiler_raw"; isDir(adjacent) && accept(adjacent) {
		return adjacent
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() || filepath.Ext(entry.Name()) != ".gpuprofiler_raw" {
			continue
		}
		if dir := filepath.Join(path, entry.Name()); accept(dir) {
			return dir
		}
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
