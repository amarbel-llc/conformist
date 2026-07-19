package format

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/stats"
	"code.linenisgreat.com/conformist/walk"
)

// formatStagedBlobs formats the given blobs (toplevel-relative path -> staged
// content) in isolation and returns the formatted content for those that
// actually changed. It materializes each blob into a private temp tree at its
// repo-relative path and runs the full format pipeline (linter repairs +
// formatters) over that tree with caching disabled, so the working tree is
// never touched. This is the partial-stage lane of --staged (#40): a file with
// both staged and unstaged changes has its STAGED blob formatted here, leaving
// the working tree's unstaged hunks alone.
//
// LIMITATION: formatters that discover config by walking up from the file
// (.editorconfig, rustfmt.toml, …) see only the temp tree, into which declared
// config files are not (yet) shipped — unlike format/sandbox.go's checkSandbox.
// Such a formatter falls back to its defaults when run against a partially
// staged file. The eng-default formatters do not rely on ancestor config, so
// this is acceptable for the spinclass per-commit-hook use case; revisit if a
// config-sensitive formatter needs partial-stage support.
func formatStagedBlobs(
	ctx context.Context,
	cfg *config.Config,
	contents map[string][]byte,
) (map[string][]byte, error) {
	if len(contents) == 0 {
		return map[string][]byte{}, nil
	}

	dir, err := os.MkdirTemp("", "conformist-staged-")
	if err != nil {
		return nil, fmt.Errorf("failed to create staged-blob temp tree: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	paths := make([]string, 0, len(contents))

	for rel, content := range contents {
		dst := filepath.Join(dir, rel)
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o700); mkErr != nil {
			return nil, fmt.Errorf("failed to create staged-blob subdir for %s: %w", rel, mkErr)
		}

		if wErr := os.WriteFile(dst, content, 0o600); wErr != nil {
			return nil, fmt.Errorf("failed to write staged blob %s: %w", rel, wErr)
		}

		paths = append(paths, dst)
	}

	// Format the temp tree in isolation: filesystem walk, no cache, quiet, and
	// none of the commit / fail-on-change knobs. A throwaway stats instance keeps
	// the temp-tree counts out of the caller's run statistics.
	isolated := *cfg
	isolated.TreeRoot = dir
	isolated.Walk = walk.Filesystem.String()
	isolated.NoCache = true
	isolated.ClearCache = false
	isolated.CI = false
	isolated.FailOnChange = false
	isolated.Quiet = true

	throwaway := stats.New()
	// The blob-isolation lane formats staged blobs in a throwaway temp tree;
	// restage-repair-outputs (conformist#55) is a working-tree concern handled by
	// the fully-staged lane, so this run uses the no-op runRepair observer and
	// ignores any reported paths.
	if _, runErr := formatTree(ctx, &isolated, &throwaway, nil, paths, runRepair); runErr != nil {
		return nil, fmt.Errorf("failed to format staged blobs: %w", runErr)
	}

	changed := make(map[string][]byte, len(contents))

	for rel, original := range contents {
		formatted, readErr := os.ReadFile(filepath.Join(dir, rel))
		if readErr != nil {
			return nil, fmt.Errorf("failed to read formatted staged blob %s: %w", rel, readErr)
		}

		if !bytes.Equal(formatted, original) {
			changed[rel] = formatted
		}
	}

	return changed, nil
}
