package format

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/format"
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
	// outside a git worktree, in stdin mode, or fail-on-change. Refusal happens
	// BEFORE any formatting. (Partially staged files are no longer refused —
	// their staged blobs are formatted in isolation; see #40.)
	ErrStagedRefused = errors.New("refusing to format staged files")
)

// RunStaged implements the lint-staged-style --staged mode (#25): format only
// the files currently staged in the index, restage the formatted content, and
// create no commit — the caller's own commit (message, signing, trailers)
// then proceeds with conformant content. Two lanes handle the two kinds of
// staged file:
//
//   - Fully staged (no unstaged delta): format the working-tree file in place
//     and `git add` exactly the files that changed. Safe because the working
//     tree and index agree, so restaging cannot sweep in unintended edits.
//   - Partially staged (staged AND carrying additional unstaged edits): format
//     the STAGED blob alone in isolation and restage it via the object store,
//     leaving the working tree's unstaged hunks untouched (graduated semantics,
//     #40). The naive "format working tree + git add" path would corrupt the
//     caller's intended commit, which is why this lane uses blob-level plumbing.
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
		partialPaths []string
		stagedSet    = make(map[string]bool)
		toFormat     []string
	)

	for _, entry := range entries {
		if entry.Staged == ' ' || entry.Staged == '?' {
			continue
		}

		if entry.Unstaged != ' ' {
			// partially staged: format the staged blob in isolation (#40). A
			// staged deletion that is also unstaged-modified has no staged
			// content to format.
			if entry.Staged != 'D' {
				partialPaths = append(partialPaths, entry.Path)
			}

			continue
		}

		stagedSet[entry.Path] = true

		// a staged deletion has no working-tree file to format
		if entry.Staged != 'D' {
			toFormat = append(toFormat, filepath.Join(cfg.TreeRoot, entry.Path))
		}
	}

	if len(toFormat) == 0 && len(partialPaths) == 0 {
		log.Debugf("--staged: nothing staged to format")

		return nil
	}

	// Fully-staged lane: format the working tree in place, restage the files
	// that changed. The partial lane below never touches the working tree.
	fullRestaged, err := restageFullyStaged(ctx, v, statz, cmd, cfg, stagedSet, toFormat)
	if err != nil {
		return err
	}

	// Partial-stage lane (#40): format each staged blob in isolation and restage
	// it via the object store, leaving the working tree's unstaged hunks alone.
	partialRestaged, err := restagePartialBlobs(ctx, cfg, partialPaths)
	if err != nil {
		return err
	}

	total := fullRestaged + partialRestaged
	if total == 0 {
		log.Debugf("--staged: staged content was already conformant")

		return nil
	}

	if !cfg.Quiet {
		fmt.Fprintf(os.Stderr, "reformatted and restaged %d staged file(s)\n", total)
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

// restageFullyStaged formats the working-tree copies of the fully-staged files
// (those with no unstaged delta) in place, then restages exactly the ones the
// run changed. It returns the number of files restaged. Safe because, with no
// pre-existing unstaged delta, any post-run worktree change on a staged file is
// formatter output.
//
// It additionally restages the outputs of opt-in restage-repair-outputs linters
// (conformist#55): a whole-tree codegen-repair linter that regenerates files
// OTHER than the staged ones would otherwise leave its output dirty-but-unstaged,
// so the commit lands stale. runWithObserver reports those paths via a
// git-status snapshot around each opt-in linter's repair, and they are restaged
// regardless of stagedSet membership — the explicit, configured intent of the
// opt-in flag.
func restageFullyStaged(
	ctx context.Context,
	v *viper.Viper,
	statz *stats.Stats,
	cmd *cobra.Command,
	cfg *config.Config,
	stagedSet map[string]bool,
	toFormat []string,
) (int, error) {
	if len(toFormat) == 0 {
		return 0, nil
	}

	slices.Sort(toFormat)

	repairOutputs, err := runWithObserver(v, statz, cmd, toFormat, stagedRepairObserver(cfg.TreeRoot))
	if err != nil {
		return 0, err
	}

	post, err := git.StatusEntries(ctx, cfg.TreeRoot)
	if err != nil {
		return 0, fmt.Errorf("failed to detect formatted files: %w", err)
	}

	// Restage the union of: the formatter/per-file restage (a staged file the run
	// left dirty) and the opt-in linters' repair outputs (restaged regardless of
	// stagedSet, conformist#55). Sort+compact dedups a path that is both.
	toRestage := slices.Clone(repairOutputs)

	for _, entry := range post {
		if entry.Unstaged != ' ' && stagedSet[entry.Path] {
			toRestage = append(toRestage, entry.Path)
		}
	}

	if len(toRestage) == 0 {
		return 0, nil
	}

	slices.Sort(toRestage)
	toRestage = slices.Compact(toRestage)

	if err := git.AddPaths(ctx, cfg.TreeRoot, toRestage); err != nil {
		return 0, fmt.Errorf("failed to restage formatted files: %w", err)
	}

	return len(toRestage), nil
}

// stagedRepairObserver returns a repairObserver that wraps an opt-in
// restage-repair-outputs linter's repair (conformist#55) in a git-status
// snapshot, reporting the toplevel-relative paths the repair newly made dirty.
// A path already dirty before the repair is excluded, so only this linter's own
// writes are captured.
//
// By default the snapshots use --untracked-files=no, so brand-new (untracked)
// outputs are NOT reported — staging untracked files is the more dangerous
// tier-3 capability. A linter that also opts into stage-new-outputs
// (conformist#56, IsStageNewOutputs) instead gets --untracked-files=all
// snapshots, so a file its repair creates appears in the after-minus-before
// delta and is staged.
func stagedRepairObserver(treeRoot string) repairObserver {
	return func(ctx context.Context, l *format.Linter, repair func(context.Context) error) ([]string, error) {
		snapshot := git.StatusEntries
		if l.IsStageNewOutputs() {
			snapshot = git.StatusEntriesWithUntracked
		}

		before, err := snapshot(ctx, treeRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to snapshot tree before %q repair: %w", l.Name(), err)
		}

		if err := repair(ctx); err != nil {
			return nil, err
		}

		after, err := snapshot(ctx, treeRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to snapshot tree after %q repair: %w", l.Name(), err)
		}

		dirtyBefore := make(map[string]bool, len(before))
		for _, e := range before {
			dirtyBefore[e.Path] = true
		}

		var outputs []string

		for _, e := range after {
			if !dirtyBefore[e.Path] {
				outputs = append(outputs, e.Path)
			}
		}

		if len(outputs) > 0 {
			log.Debugf("--staged: linter %q repair wrote %d path(s) to restage", l.Name(), len(outputs))
		}

		return outputs, nil
	}
}

// restagePartialBlobs formats the staged blob of each partially-staged file in
// isolation and restages the formatted content via the object store
// (hash-object + update-index --cacheinfo), preserving each file's index mode
// and leaving the working tree's unstaged hunks untouched (#40). It returns the
// number of files restaged.
func restagePartialBlobs(ctx context.Context, cfg *config.Config, paths []string) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}

	slices.Sort(paths)

	contents := make(map[string][]byte, len(paths))
	modes := make(map[string]string, len(paths))

	for _, p := range paths {
		blob, err := git.StagedBlob(ctx, cfg.TreeRoot, p)
		if err != nil {
			return 0, fmt.Errorf("failed to read staged blob: %w", err)
		}

		mode, err := git.StagedFileMode(ctx, cfg.TreeRoot, p)
		if err != nil {
			return 0, fmt.Errorf("failed to read staged mode: %w", err)
		}

		contents[p] = blob
		modes[p] = mode
	}

	changed, err := formatStagedBlobs(ctx, cfg, contents)
	if err != nil {
		return 0, err
	}

	if len(changed) == 0 {
		return 0, nil
	}

	restaged := make([]string, 0, len(changed))
	for p := range changed {
		restaged = append(restaged, p)
	}

	slices.Sort(restaged)

	for _, p := range restaged {
		oid, err := git.HashObject(ctx, cfg.TreeRoot, p, changed[p])
		if err != nil {
			return 0, fmt.Errorf("failed to write formatted staged blob: %w", err)
		}

		if err := git.UpdateIndexCacheinfo(ctx, cfg.TreeRoot, modes[p], oid, p); err != nil {
			return 0, fmt.Errorf("failed to restage formatted staged blob: %w", err)
		}
	}

	return len(restaged), nil
}
