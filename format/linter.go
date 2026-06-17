package format

import (
	"context"
	"fmt"
	"time"

	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/walk"
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

	args := append([]string{}, options...)

	// A whole-tree check (passes-files=false) runs once with no file arguments;
	// the matched files only gate whether it runs. Otherwise pass each matched
	// file's path as a positional argument.
	if l.passesFiles {
		if len(files) > 1 && l.hasNoPositionalArgSupport() {
			return false, "", ErrNoPositionalArgSupport
		}

		for _, file := range files {
			args = append(args, relocateFileArg(l.treeRoot, l.workingDir, file.RelPath, ""))
		}
	}

	start := time.Now()

	l.log.Debugf("executing: %s %v", inv.raw, args)

	// inv.run maps a non-zero exit to nonzero=true (findings) and reserves err
	// for an operational failure (RFC 0001 §4).
	nonzero, output, err = inv.run(ctx, l.workingDir, args)
	if err != nil {
		return false, output, err
	}

	if l.passesFiles {
		l.log.Infof("%v file(s) checked in %v", len(files), time.Since(start))
	} else {
		l.log.Infof("whole-tree check completed in %v", time.Since(start))
	}

	return nonzero, output, nil
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
