package format

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/stats"
	"github.com/amarbel-llc/conformist/walk"
	"github.com/charmbracelet/log"
	"github.com/gobwas/glob"
	"mvdan.cc/sh/v3/expand"
)

// CompositeLinter applies linter repair (autofix) commands in repair mode
// (RFC 0001 §4). Only linters that declare a repair command are included;
// check-only linters are a no-op in repair mode.
type CompositeLinter struct {
	stats          *stats.Stats
	globalExcludes []glob.Glob
	linters        map[string]*Linter
}

// Empty reports whether there are no repair-capable linters, allowing callers to
// skip a tree walk entirely.
func (c *CompositeLinter) Empty() bool {
	return len(c.linters) == 0
}

// Repair runs each repair-capable linter's autofix command over the files it
// matches. It may write to the files. A non-nil error indicates an operational
// failure.
func (c *CompositeLinter) Repair(ctx context.Context, files []*walk.File) error {
	linterFiles := map[*Linter][]*walk.File{}

	for _, file := range files {
		// A globally-excluded file is still offered to linters that opt out of
		// global excludes via ignore-global-excludes (conformist#44); others
		// skip it as before.
		globallyExcluded := pathMatches(file.RelPath, c.globalExcludes)

		for _, l := range c.linters {
			if globallyExcluded && !l.IgnoresGlobalExcludes() {
				continue
			}

			if l.Wants(file) {
				linterFiles[l] = append(linterFiles[l], file)
			}
		}
	}

	for l, fs := range linterFiles {
		c.stats.Add(stats.Matched, len(fs))

		if err := l.Repair(ctx, fs); err != nil {
			return fmt.Errorf("linter %q repair failed: %w", l.Name(), err)
		}
	}

	return nil
}

// NewCompositeLinter builds a repair-mode linter set, including only linters
// that declare a repair command.
func NewCompositeLinter(cfg *config.Config, statz *stats.Stats) (*CompositeLinter, error) {
	globalExcludes, err := compileGlobs(cfg.Excludes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile global excludes: %w", err)
	}

	env := expand.ListEnviron(os.Environ()...)

	linters := map[string]*Linter{}

	for name, lCfg := range cfg.LinterConfigs {
		if lCfg.RepairCommand == "" {
			// check-only linter: nothing to do in repair mode
			continue
		}

		linter, err := newLinter(name, cfg.TreeRoot, env, lCfg)
		if errors.Is(err, ErrCommandNotFound) && cfg.AllowMissingFormatter {
			log.Debugf("linter command not found: %v", name)

			continue
		} else if err != nil {
			return nil, fmt.Errorf("failed to initialise linter %v: %w", name, err)
		}

		linters[name] = linter
	}

	return &CompositeLinter{
		stats:          statz,
		globalExcludes: globalExcludes,
		linters:        linters,
	}, nil
}
