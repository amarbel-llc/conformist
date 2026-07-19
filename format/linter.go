package format

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/walk"
	"github.com/charmbracelet/log"
	"github.com/gobwas/glob"
	"mvdan.cc/sh/v3/expand"
)

// Linter wraps a configured [linter.<name>] tool. Its check command is read-only
// (RFC 0001 §4); an optional repair command applies autofixes in repair mode.
type Linter struct {
	name   string
	config *config.Linter

	log         *log.Logger
	commandInv  invocation // resolved check Command (bare exe or shell line, #38)
	repairInv   invocation // resolved RepairCommand (zero value if none configured)
	treeRoot    string     // the project tree root (working-dir is resolved against it)
	workingDir  string     // the dir the tool runs in: treeRoot, or a subdir (#38)
	passesFiles bool       // false => whole-tree check: run once, no file args

	includes []glob.Glob
	excludes []glob.Glob
}

func (l *Linter) Name() string { return l.name }

func (l *Linter) Priority() int { return l.config.Priority }

// HasRepair reports whether a repair (autofix) command is configured.
func (l *Linter) HasRepair() bool { return l.config.RepairCommand != "" }

// IsRestageRepairOutputs reports whether this is a whole-tree
// (passes-files=false) linter that opts into restaging its repair outputs and
// actually has a repair command to produce them (conformist#55). The staged
// lane runs exactly these linters under an individual git-status snapshot so it
// can restage their writes; every other linter stays on the safe staged-set
// scoping.
func (l *Linter) IsRestageRepairOutputs() bool {
	return l.config.RestageRepairOutputs && !l.passesFiles && l.HasRepair()
}

// IsStageNewOutputs reports whether this linter additionally opts into staging
// the brand-new (untracked) files its repair-command creates (tier 3,
// conformist#56, RFC-0002 §2.3). It is effective only when the linter already
// qualifies for tier-2 restaging (IsRestageRepairOutputs): staging untracked
// files is a strictly more dangerous capability, so it is gated behind both the
// tier-2 opt-in and its own flag.
func (l *Linter) IsStageNewOutputs() bool {
	return l.config.StageNewOutputs && l.IsRestageRepairOutputs()
}

// IsStageDeletedOutputs reports whether this linter opts into staging the
// deletions its repair-command performs (tier 4, conformist#57, RFC-0002 §2.4).
// Like tier 3 it is effective only when the linter qualifies for tier-2
// restaging (IsRestageRepairOutputs): staging a deletion removes a path from the
// commit's tree, the most destructive stage mutation, so it is gated behind both
// the tier-2 opt-in and its own flag. Tiers 2 and 3 MUST NOT stage deletions.
func (l *Linter) IsStageDeletedOutputs() bool {
	return l.config.StageDeletedOutputs && l.IsRestageRepairOutputs()
}

func (l *Linter) hasNoPositionalArgSupport() bool {
	return l.config.NoPositionalArgSupport != nil && *l.config.NoPositionalArgSupport
}

// Wants reports whether this linter should inspect the given file, per its
// includes/excludes globs.
func (l *Linter) Wants(file *walk.File) bool {
	return !pathMatches(file.RelPath, l.excludes) && pathMatches(file.RelPath, l.includes)
}

// Check runs the linter's read-only check command over files. It returns true if
// the linter reported findings (a non-zero exit), along with the combined
// output. A non-nil error indicates an operational failure, not findings.
func (l *Linter) Check(ctx context.Context, files []*walk.File) (findings bool, output string, err error) {
	return l.run(ctx, l.commandInv, l.config.Options, files)
}

// Repair runs the linter's autofix command over files (it may write to them).
// It is a no-op when no repair command is configured.
func (l *Linter) Repair(ctx context.Context, files []*walk.File) error {
	if l.config.RepairCommand == "" {
		return nil
	}

	_, output, err := l.run(ctx, l.repairInv, l.config.RepairOptions, files)
	if err != nil {
		return err
	}

	if output != "" {
		l.log.Debug(output)
	}

	return nil
}

func (l *Linter) run(
	ctx context.Context, inv invocation, options []string, files []*walk.File,
) (nonzero bool, output string, err error) {
	if len(files) == 0 {
		return false, "", nil
	}

	start := time.Now()

	// A whole-tree check (passes-files=false) runs once with no file arguments;
	// the matched files only gate whether it runs.
	if !l.passesFiles {
		args := append([]string{}, options...)

		l.log.Debugf("executing: %s %v", inv.raw, args)

		// inv.run maps a non-zero exit to nonzero=true (findings) and reserves
		// err for an operational failure (RFC 0001 §4).
		nonzero, output, err = inv.run(ctx, l.workingDir, args)
		if err != nil {
			return false, output, err
		}

		l.log.Infof("whole-tree check completed in %v", time.Since(start))

		return nonzero, output, nil
	}

	// A no-positional-arg-support tool (e.g. statix) takes exactly one file per
	// invocation, so chunk to size 1 — the same batching the formatter paths
	// apply in scheduler.go and sandbox.go — rather than failing a multi-file
	// match (conformist#87). This covers the check and repair lanes alike.
	maxBatch := len(files)
	if l.hasNoPositionalArgSupport() {
		maxBatch = 1
	}

	var combined strings.Builder

	for chunk := range slices.Chunk(files, maxBatch) {
		args := append([]string{}, options...)
		for _, file := range chunk {
			args = append(args, relocateFileArg(l.treeRoot, l.workingDir, file.RelPath, ""))
		}

		l.log.Debugf("executing: %s %v", inv.raw, args)

		chunkNonzero, out, runErr := inv.run(ctx, l.workingDir, args)
		combined.WriteString(out)

		if runErr != nil {
			return false, combined.String(), runErr
		}

		nonzero = nonzero || chunkNonzero
	}

	l.log.Infof("%v file(s) checked in %v", len(files), time.Since(start))

	return nonzero, combined.String(), nil
}

// newLinter creates a Linter, resolving its check (and optional repair)
// executables and compiling its include/exclude globs.
func newLinter(name, treeRoot string, env expand.Environ, cfg *config.Linter) (*Linter, error) {
	if !nameRegex.MatchString(name) {
		return nil, ErrInvalidName
	}

	l := Linter{
		name:        name,
		config:      cfg,
		treeRoot:    treeRoot,
		workingDir:  resolveToolDir(treeRoot, cfg.WorkingDir),
		passesFiles: cfg.PassesFiles == nil || *cfg.PassesFiles,
	}

	var err error

	l.commandInv, err = newInvocation(name, treeRoot, env, cfg.Command)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCommandNotFound, err)
	}

	if cfg.RepairCommand != "" {
		l.repairInv, err = newInvocation(name+" (repair)", treeRoot, env, cfg.RepairCommand)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCommandNotFound, err)
		}
	}

	if cfg.Priority > 0 {
		l.log = log.WithPrefix(fmt.Sprintf("linter | %s[%d]", name, cfg.Priority))
	} else {
		l.log = log.WithPrefix("linter | " + name)
	}

	if len(cfg.Includes) == 0 {
		return nil, fmt.Errorf("linter '%v' has no includes", l.name)
	}

	l.includes, err = compileGlobs(cfg.Includes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile linter '%v' includes: %w", l.name, err)
	}

	l.excludes, err = compileGlobs(cfg.Excludes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile linter '%v' excludes: %w", l.name, err)
	}

	return &l, nil
}
