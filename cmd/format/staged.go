package format

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/git"
	"github.com/amarbel-llc/conformist/stats"
	"github.com/amarbel-llc/conformist/walk"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	// ErrFixesRestaged signals that --staged reformatted staged files and
	// restaged the formatted content. Like ErrFixesCommitted it is exit-code
	// signalling (3), not a failure — the caller's own commit then proceeds
	// with conformant content.
	ErrFixesRestaged = errors.New("fixes were applied and restaged")

	// ErrStagedRefused indicates --staged declined to run (exit code 2):
	// outside a git worktree, in stdin mode, fail-on-change, or partially
	// staged files (the "partially staged" message token is grep-stable for
	// hook consumers). Refusal happens BEFORE any formatting.
	ErrStagedRefused = errors.New("refusing to format staged files")
)

// RunStaged implements the lint-staged-style --staged mode (#25): format only
// the files currently staged in the index, restage the formatted content, and
// create no commit — the caller's own commit (message, signing, trailers)
// then proceeds with conformant content. Files that are staged AND carry
// additional unstaged edits are refused up front: formatting the working tree
// and restaging would sweep the unstaged hunks into the index, corrupting the
// caller's intended commit. (Formatting the staged blobs alone would be the
// graduated semantics; see #25.)
//
// exitZeroOnFix (#35/#39) downgrades the restage's exit-3 signal to exit 0, for
// callers that gate on "nonzero = abort" — e.g. a git pre-commit hook, where a
// successful restage is the SUCCESS path and the commit should proceed with the
// formatted content. Refusals (ErrStagedRefused) and operational errors stay
// nonzero. Mirrors RunCommit's ExitZeroOnFix.
func RunStaged(v *viper.Viper, statz *stats.Stats, cmd *cobra.Command, paths []string, exitZeroOnFix bool) error {
	cmd.SilenceUsage = true

	// the staged set IS the scope; explicit paths have nothing to select
	if len(paths) > 0 {
		return errors.New("positional paths cannot be combined with --staged")
	}

	cfg, err := config.FromViper(v)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if walkType, typeErr := walk.TypeString(cfg.Walk); typeErr == nil && walkType == walk.Stdin {
		return fmt.Errorf("%w: stdin mode has no index to format", ErrStagedRefused)
	}

	// fail-on-change (implied by --ci) would error after the files were
	// already rewritten, stranding unstaged fixes — same trap as --commit.
	if cfg.FailOnChange {
		return fmt.Errorf("%w: fail-on-change (implied by --ci) contradicts restaging fixes", ErrStagedRefused)
	}

	insideWorktree, err := git.IsInsideWorktree(cfg.TreeRoot)
	if err != nil {
		return fmt.Errorf("failed to check for a git worktree: %w", err)
	}

	if !insideWorktree {
		return fmt.Errorf("%w: %s is not inside a git worktree", ErrStagedRefused, cfg.TreeRoot)
	}

	entries, err := git.StatusEntries(ctx, cfg.TreeRoot)
	if err != nil {
		return fmt.Errorf("failed to detect staged files: %w", err)
	}

	var (
		partial   []string
		stagedSet = make(map[string]bool)
		toFormat  []string
	)

	for _, entry := range entries {
		if entry.Staged == ' ' || entry.Staged == '?' {
			continue
		}

		if entry.Unstaged != ' ' {
			partial = append(partial, entry.Path)

			continue
		}

		stagedSet[entry.Path] = true

		// a staged deletion has no working-tree file to format
		if entry.Staged != 'D' {
			toFormat = append(toFormat, filepath.Join(cfg.TreeRoot, entry.Path))
		}
	}

	if len(partial) > 0 {
		slices.Sort(partial)

		return fmt.Errorf(
			"%w: partially staged (staged with additional unstaged changes): %s; "+
				"stage the remaining changes or commit in two passes",
			ErrStagedRefused, strings.Join(partial, ", "),
		)
	}

	if len(toFormat) == 0 {
		log.Debugf("--staged: nothing staged to format")

		return nil
	}

	slices.Sort(toFormat)

	if err := Run(v, statz, cmd, toFormat); err != nil {
		return err
	}

	// anything in the staged set that now differs from the index is formatter
	// output (the partial-staging refusal above guarantees there were no
	// pre-existing unstaged deltas on these files) — restage exactly that.
	post, err := git.StatusEntries(ctx, cfg.TreeRoot)
	if err != nil {
		return fmt.Errorf("failed to detect formatted files: %w", err)
	}

	var toRestage []string

	for _, entry := range post {
		if entry.Unstaged != ' ' && stagedSet[entry.Path] {
			toRestage = append(toRestage, entry.Path)
		}
	}

	if len(toRestage) == 0 {
		log.Debugf("--staged: staged content was already conformant")

		return nil
	}

	slices.Sort(toRestage)

	if err := git.AddPaths(ctx, cfg.TreeRoot, toRestage); err != nil {
		return fmt.Errorf("failed to restage formatted files: %w", err)
	}

	if !cfg.Quiet {
		fmt.Fprintf(os.Stderr, "reformatted and restaged %d staged file(s)\n", len(toRestage))
	}

	// --exit-zero-on-fix (#35/#39): a successful restage is success, not exit 3,
	// for callers that gate on "nonzero = abort" (e.g. a git pre-commit hook).
	// The summary above still prints; only the exit code changes.
	if exitZeroOnFix {
		return nil
	}

	// ErrFixesRestaged is exit-code signalling (3), not a failure to print.
	cmd.SilenceErrors = true

	return ErrFixesRestaged
}
