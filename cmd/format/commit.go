package format

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// CommitMessage is the conventional message for auto-applied fixes (#24).
const CommitMessage = "chore: conformist fmt+fix"

var (
	// ErrFixesCommitted signals that --commit applied fixes and created a
	// commit. Not a failure: it flows through the error channel so main can
	// map it to exit code 3, distinct from 0 (tree was already conformant),
	// 1 (findings / other errors) and 2 (refused or operational failure).
	ErrFixesCommitted = errors.New("fixes were applied and committed")

	// ErrCommitRefused indicates --commit declined to run (exit code 2):
	// outside a git worktree, in stdin mode, or on an unclean working tree
	// without --allow-dirty. Refusal happens BEFORE any formatting, so a
	// refused run leaves the tree untouched.
	ErrCommitRefused = errors.New("refusing to format and commit")

	// ErrConflictMarkers indicates --commit refused to commit because the
	// content it was about to commit carries leftover merge-conflict markers
	// (exit code 2, #67). Unlike a formatting fix, a conflict is not something
	// --exit-zero-on-fix may swallow: the guard runs before that downgrade, so
	// a conflicted tree always halts the caller rather than burying a
	// non-building commit (especially via --amend) in history.
	ErrConflictMarkers = errors.New("refusing to commit content with leftover conflict markers")
)

// CommitOptions carries the --commit flow's knobs from the CLI flags.
type CommitOptions struct {
	// AllowDirty admits an unclean working tree; pre-dirty files are excluded
	// from the fix commit.
	AllowDirty bool
	// Trailers are appended to the commit message via `git commit --trailer`
	// (#26), e.g. a tool-attribution line.
	Trailers []string
	// Amend folds the run's fixes into HEAD via `git commit --amend --no-edit`
	// (#33) instead of creating a fresh CommitMessage commit, keeping the
	// existing message. Refused when HEAD is already pushed or absent.
	Amend bool
	// ExitZeroOnFix returns exit 0 instead of 3 when fixes were committed/amended
	// (#35), so a caller that treats any nonzero exit as failure — e.g. a
	// spinclass pre-merge repair hook — sees success. Refusals and operational
	// failures still exit nonzero.
	ExitZeroOnFix bool
}

// RunCommit wraps Run with the --commit flow (#24): verify the tree is safe
// to auto-commit, format/repair in place, then commit exactly the files the
// run changed as a `chore: conformist fmt+fix` commit. git itself is the
// change detector — the pre/post `git status` delta — because the formatter
// pipeline's own change accounting does not see linter-repair writes.
func RunCommit(v *viper.Viper, statz *stats.Stats, cmd *cobra.Command, paths []string, opts CommitOptions) error {
	cmd.SilenceUsage = true

	cfg, err := config.FromViper(v)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	preDirty, err := commitPreflight(ctx, cfg, opts.AllowDirty, opts.Amend)
	if err != nil {
		return err
	}

	if err := Run(v, statz, cmd, paths); err != nil {
		return err
	}

	post, err := git.ChangedPaths(ctx, cfg.TreeRoot)
	if err != nil {
		return fmt.Errorf("failed to detect changed files: %w", err)
	}

	// commit only files this run changed: anything dirty before the run
	// (admitted via --allow-dirty) is excluded, even if a formatter changed
	// it further — its diff would mix user work with fixes.
	toCommit := make([]string, 0, len(post))

	for _, p := range post {
		if !preDirty[p] {
			toCommit = append(toCommit, p)
		}
	}

	if len(toCommit) == 0 {
		log.Debugf("--commit: no fixes were needed")

		return nil
	}

	slices.Sort(toCommit)

	// Guard (#67): never let a fix commit carry leftover conflict markers. The
	// commit takes its content from the working tree, so if any to-be-committed
	// file holds `<<<<<<<`/`=======`/`>>>>>>>` (e.g. an autostash-pop conflict
	// left markers in a file this run also reformatted), refuse here — before
	// the --exit-zero-on-fix downgrade below — with a nonzero exit. A conflict
	// is not a fixable formatting issue, and committing it (especially via
	// --amend) buries a non-building tree in history.
	conflicted, err := git.ConflictMarkerPaths(ctx, cfg.TreeRoot, toCommit)
	if err != nil {
		return fmt.Errorf("failed to check for conflict markers: %w", err)
	}

	if len(conflicted) > 0 {
		return fmt.Errorf(
			"%w: %s", ErrConflictMarkers, strings.Join(conflicted, ", "),
		)
	}

	// A failed commit (e.g. the signing agent is locked) must fail loudly:
	// both CommitPaths and AmendPaths surface git's stderr, create no commit,
	// and leave the index untouched (`git commit -- <paths>` stages nothing on
	// failure).
	sha, err := commitOrAmend(ctx, cfg.TreeRoot, opts, toCommit)
	if err != nil {
		return fmt.Errorf("failed to commit fixes: %w", err)
	}

	if !cfg.Quiet {
		if opts.Amend {
			fmt.Fprintf(os.Stderr, "amended HEAD with %d fixed file(s) (%s)\n", len(toCommit), sha)
		} else {
			fmt.Fprintf(os.Stderr, "committed %d fixed file(s) as %s (%s)\n", len(toCommit), sha, CommitMessage)
		}
	}

	// --exit-zero-on-fix (#35): a successful repair is success, not exit 3, for
	// callers that gate on "nonzero = abort" (e.g. a spinclass pre-merge repair
	// hook). The summary above still prints; only the exit code changes.
	if opts.ExitZeroOnFix {
		return nil
	}

	// ErrFixesCommitted is exit-code signalling (3), not a failure to print.
	cmd.SilenceErrors = true

	return ErrFixesCommitted
}

// commitOrAmend creates the fix commit (or amends HEAD with #33's --amend),
// taking each path's content from the working tree. Returns the resulting HEAD
// hash.
func commitOrAmend(ctx context.Context, treeRoot string, opts CommitOptions, paths []string) (string, error) {
	if opts.Amend {
		return git.AmendPaths(ctx, treeRoot, opts.Trailers, paths) //nolint:wrapcheck
	}

	return git.CommitPaths(ctx, treeRoot, CommitMessage, opts.Trailers, paths) //nolint:wrapcheck
}

// commitPreflight enforces the --commit safety policy and returns the set of
// paths that were already dirty before the run. Current policy: refuse on ANY
// tracked staged/unstaged change unless --allow-dirty is passed (untracked
// files are ignored throughout — they are never committed). With amend (#33),
// it additionally refuses when HEAD has no commit to amend or is already
// pushed.
//
// NOTE(#24, agent-loop): this policy is deliberately isolated here. It is a
// first cut optimized for the pre-merge-hook case, where the tree is clean by
// construction; once the flag has seen real agent-loop use the
// refuse/allow-dirty split may need revisiting (e.g. scoping dirtiness to
// formatter-matched files) without touching the commit flow itself.
func commitPreflight(ctx context.Context, cfg *config.Config, allowDirty, amend bool) (map[string]bool, error) {
	if walkType, typeErr := walk.TypeString(cfg.Walk); typeErr == nil && walkType == walk.Stdin {
		return nil, fmt.Errorf("%w: stdin mode has no working tree state to commit", ErrCommitRefused)
	}

	// fail-on-change wants changes to fail the run; --commit wants them
	// committed. The cobra flag exclusion only catches the literal flag pair;
	// this catches it arriving via env, config, or --ci (which implies it) —
	// otherwise the run would format, error, and strand uncommitted fixes.
	if cfg.FailOnChange {
		return nil, fmt.Errorf(
			"%w: fail-on-change (implied by --ci) contradicts committing the changes", ErrCommitRefused,
		)
	}

	insideWorktree, err := git.IsInsideWorktree(cfg.TreeRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to check for a git worktree: %w", err)
	}

	if !insideWorktree {
		return nil, fmt.Errorf("%w: %s is not inside a git worktree", ErrCommitRefused, cfg.TreeRoot)
	}

	// amend rewrites HEAD: refuse if there is no HEAD to amend, or if HEAD is
	// already published (amending would rewrite shared history). These run
	// before any formatting, so a refused amend leaves the tree untouched.
	if amend {
		if err := amendPreflight(ctx, cfg.TreeRoot); err != nil {
			return nil, err
		}
	}

	dirty, err := git.ChangedPaths(ctx, cfg.TreeRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to detect uncommitted changes: %w", err)
	}

	if len(dirty) > 0 && !allowDirty {
		return nil, fmt.Errorf(
			"%w: the working tree has uncommitted changes to %d tracked file(s); "+
				"commit or stash them, or pass --allow-dirty to commit only files this run changes",
			ErrCommitRefused, len(dirty),
		)
	}

	preDirty := make(map[string]bool, len(dirty))
	for _, p := range dirty {
		preDirty[p] = true
	}

	return preDirty, nil
}

// amendPreflight rejects an --amend run that cannot safely rewrite HEAD: a repo
// with no commit to amend, or a HEAD already pushed to a remote (amending would
// rewrite published history).
func amendPreflight(ctx context.Context, treeRoot string) error {
	headExists, err := git.HeadExists(ctx, treeRoot)
	if err != nil {
		return fmt.Errorf("failed to check for a HEAD commit: %w", err)
	}

	if !headExists {
		return fmt.Errorf("%w: nothing to amend (no HEAD commit yet)", ErrCommitRefused)
	}

	remoteRefs, err := git.HeadRemoteRefs(ctx, treeRoot)
	if err != nil {
		return fmt.Errorf("failed to check whether HEAD is pushed: %w", err)
	}

	if len(remoteRefs) > 0 {
		return fmt.Errorf(
			"%w: HEAD is already pushed (%s); refusing to amend published history",
			ErrCommitRefused, strings.Join(remoteRefs, ", "),
		)
	}

	return nil
}
