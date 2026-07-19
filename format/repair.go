package format

import (
	"context"
	"errors"
	"fmt"
	"os"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/stats"
	"code.linenisgreat.com/conformist/walk"
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
//
// Linters that opt into restaging their repair outputs (conformist#55,
// [Linter.IsRestageRepairOutputs]) are SKIPPED here: they are run separately,
// once over their full accumulated match set, by [CompositeLinter.RepairLinter]
// — the staged lane wraps that call in an individual git-status snapshot so it
// can attribute and restage their writes, while a plain repair run invokes it
// with the snapshot ignored. Either way they run exactly once.
func (c *CompositeLinter) Repair(ctx context.Context, files []*walk.File) error {
	for l, fs := range c.match(files) {
		if l.IsRestageRepairOutputs() {
			continue
		}

		c.stats.Add(stats.Matched, len(fs))

		if err := l.Repair(ctx, fs); err != nil {
			return fmt.Errorf("linter %q repair failed: %w", l.Name(), err)
		}
	}

	return nil
}

// MatchOptInRepair returns, for the restage-repair-outputs opt-in linters only
// (conformist#55), the files each one matches in this batch. The repair walk
// accumulates these across the whole tree, then runs each opt-in linter once
// via [CompositeLinter.RepairLinter]. Returns nil when no linter opts in, so the
// common case allocates nothing.
func (c *CompositeLinter) MatchOptInRepair(files []*walk.File) map[*Linter][]*walk.File {
	var matched map[*Linter][]*walk.File

	for l, fs := range c.match(files) {
		if !l.IsRestageRepairOutputs() {
			continue
		}

		if matched == nil {
			matched = map[*Linter][]*walk.File{}
		}

		matched[l] = append(matched[l], fs...)
	}

	return matched
}

// RepairLinter runs a single linter's autofix command over files and records
// the match in stats. Used by the repair walk to run an opt-in repair linter
// (conformist#55) on its full accumulated match set — under a git-status
// snapshot in the staged lane, or directly in a plain repair run.
func (c *CompositeLinter) RepairLinter(ctx context.Context, l *Linter, files []*walk.File) error {
	if len(files) == 0 {
		return nil
	}

	c.stats.Add(stats.Matched, len(files))

	if err := l.Repair(ctx, files); err != nil {
		return fmt.Errorf("linter %q repair failed: %w", l.Name(), err)
	}

	return nil
}

// match groups the batch's files by the linters that want them, applying the
// global-excludes rule: a globally-excluded file is offered only to whole-tree
// checks (passes-files=false), whose includes are a trigger gate rather than an
// input set; per-file linters skip it, mirroring the formatter "don't rewrite"
// intent (conformist#45, retiring the conformist#44 flag).
func (c *CompositeLinter) match(files []*walk.File) map[*Linter][]*walk.File {
	linterFiles := map[*Linter][]*walk.File{}

	for _, file := range files {
		globallyExcluded := pathMatches(file.RelPath, c.globalExcludes)

		for _, l := range c.linters {
			if globallyExcluded && l.passesFiles {
				continue
			}

			if l.Wants(file) {
				linterFiles[l] = append(linterFiles[l], file)
			}
		}
	}

	return linterFiles
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
		if errors.Is(err, ErrCommandNotFound) && !cfg.RequireTools {
			// Repair mode degrades on a missing tool binary: skip this linter's repair
			// lane with a LOUD warning rather than aborting the whole run, so a repair
			// that only needs the formatter (or other linter) lanes still proceeds —
			// the motivating conformist#75 case (a dep-bump repair dying on an
			// unrelated missing linter binary). --require-tools restores strict failure
			// for gates.
			log.Warnf(
				"linter %q repair skipped: %v — repair lane degraded; pass --require-tools to fail instead",
				name, err,
			)

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
