package main

import (
	"fmt"
	"path/filepath"
)

func rejectRecordingDestinations(sourceFiles, recordingPaths []string) error {
	for _, input := range sourceFiles {
		candidates := []string{input, resolveExistingPrefix(input)}
		for _, recording := range recordingPaths {
			recordings := []string{recording, resolveExistingPrefix(recording)}
			for _, candidate := range candidates {
				for _, destination := range recordings {
					if !pathsOverlap(candidate, destination) {
						continue
					}
					return fmt.Errorf("run: recording destination %s overlaps source input %s", recording, input)
				}
			}
		}
	}
	return nil
}

func resolveExistingPrefix(path string) string {
	current := filepath.Clean(path)
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(path)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func pathsOverlap(a, b string) bool {
	return pathWithin(a, b) || pathWithin(b, a)
}
