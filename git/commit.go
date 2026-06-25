package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// StatusEntry is one `git status --porcelain` row for a tracked path: Staged
// is the index (X) column, Unstaged the worktree (Y) column.
type StatusEntry struct {
	Staged   byte
	Unstaged byte
	Path     string
}

// StatusEntries returns the tracked paths with staged or unstaged changes, as
// reported by `git status --porcelain -z --untracked-files=no`,
// toplevel-relative. Untracked files are excluded by design: neither the
// --commit (#24) nor the --staged (#25) flow touches paths git does not
// already track.
func StatusEntries(ctx context.Context, treeRoot string) ([]StatusEntry, error) {
	return statusEntries(ctx, treeRoot, "no")
}

// StatusEntriesWithUntracked is StatusEntries plus untracked files
// (`--untracked-files=all`, so files inside untracked directories are listed
// individually rather than collapsed into a single directory entry). Untracked
// entries carry `?` in both status columns. Used by the tier-3 staged lane
// (conformist#56) to attribute brand-new repair outputs by status delta.
func StatusEntriesWithUntracked(ctx context.Context, treeRoot string) ([]StatusEntry, error) {
	return statusEntries(ctx, treeRoot, "all")
}

// statusEntries runs `git status --porcelain -z` with the given
// --untracked-files mode and parses the toplevel-relative entries.
func statusEntries(ctx context.Context, treeRoot, untracked string) ([]StatusEntry, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", treeRoot, "status", "--porcelain", "-z", "--untracked-files="+untracked)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to read git status for %s: %w", treeRoot, err)
	}

	var entries []StatusEntry

	// -z entries are "XY <path>" separated by NUL; a rename/copy ("R"/"C" in
	// the staged column) is followed by its origin path as an extra NUL field,
	// which we skip — the current path is what a fix would operate on.
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}

		entries = append(entries, StatusEntry{
			Staged:   entry[0],
			Unstaged: entry[1],
			Path:     entry[3:],
		})

		if entry[0] == 'R' || entry[0] == 'C' {
			i++
		}
	}

	return entries, nil
}

// ChangedPaths returns the toplevel-relative paths of tracked files with
// staged or unstaged changes. See StatusEntries.
func ChangedPaths(ctx context.Context, treeRoot string) ([]string, error) {
	entries, err := StatusEntries(ctx, treeRoot)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}

	return paths, nil
}

// AddPaths stages the given toplevel-relative paths (`git add`), anchored to
// the repository toplevel via the ":(top)" pathspec magic like CommitPaths.
func AddPaths(ctx context.Context, treeRoot string, paths []string) error {
	args := make([]string, 0, 4+len(paths))
	args = append(args, "-C", treeRoot, "add", "--")

	for _, p := range paths {
		args = append(args, ":(top)"+p)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add failed: %w: %s", err, out)
	}

	return nil
}

// conflictMarkerMessage is the warning `git diff --check` prints for a line it
// recognizes as a leftover merge-conflict marker (`<<<<<<<`, `|||||||`,
// `=======`, `>>>>>>>`). git's --check also flags whitespace errors with
// different messages; matching this exact suffix isolates conflict markers,
// which (unlike a whitespace nit) are never a fixable formatting issue.
const conflictMarkerMessage = ": leftover conflict marker"

// ConflictMarkerPaths returns, among the given toplevel-relative paths, those
// whose working-tree content carries leftover merge-conflict markers, as
// detected by `git diff --check` (working tree against the index). For files
// that were clean in the index before a run — exactly the files the --commit
// flow commits (#67) — the diff covers their full to-be-committed change, so a
// marker anywhere in such a file is seen. Only git's "leftover conflict marker"
// warnings are reported; whitespace warnings are ignored. The returned paths
// are in git's own output form (relative to treeRoot).
func ConflictMarkerPaths(ctx context.Context, treeRoot string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	args := make([]string, 0, 5+len(paths))
	args = append(args, "-C", treeRoot, "diff", "--check", "--")

	for _, p := range paths {
		args = append(args, ":(top)"+p)
	}

	// `git diff --check` exits non-zero precisely when it finds problems
	// (conflict markers or whitespace errors), writing one warning line per
	// problem to stdout. That non-zero exit is the signal we want, not a
	// failure — so an ExitError is expected and its captured stdout is parsed;
	// only a non-ExitError (git missing, not a repo) is a real error.
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git diff --check failed in %s: %w", treeRoot, err)
		}
	}

	var (
		conflicted []string
		seen       = make(map[string]bool)
	)

	for line := range strings.SplitSeq(string(out), "\n") {
		// each warning is "<path>:<lineno>: <message>"; keep only the
		// conflict-marker message, then strip ":<lineno>: <message>" to recover
		// the path.
		if !strings.HasSuffix(line, conflictMarkerMessage) {
			continue
		}

		lineNoSep := strings.LastIndex(strings.TrimSuffix(line, conflictMarkerMessage), ":")
		if lineNoSep < 0 {
			continue
		}

		path := line[:lineNoSep]
		if !seen[path] {
			seen[path] = true
			conflicted = append(conflicted, path)
		}
	}

	return conflicted, nil
}

// CommitPaths creates a commit containing exactly the given toplevel-relative
// paths, taking their content from the working tree (`git commit -- <paths>`).
// Unrelated staged changes are left in the index untouched. Invoking the real
// git binary means the repo's commit-signing and identity config are honored.
// Each trailer is appended to the message via `git commit --trailer`, which
// also validates it (a malformed trailer fails the commit). Returns the new
// commit's hash.
func CommitPaths(
	ctx context.Context,
	treeRoot string,
	message string,
	trailers []string,
	paths []string,
) (string, error) {
	return commitWithPaths(ctx, treeRoot, []string{"commit", "--quiet", "-m", message}, trailers, paths)
}

// AmendPaths folds the given toplevel-relative paths' working-tree content into
// HEAD (`git commit --amend --no-edit -- <paths>`): the existing message is
// kept (no editor), and only the listed paths are updated in the amended tree —
// the rest of HEAD's tree is preserved. Like CommitPaths, the real git binary
// honors the repo's signing/identity config, and trailers are appended (here to
// the kept message). Returns the amended commit's new hash.
func AmendPaths(
	ctx context.Context,
	treeRoot string,
	trailers []string,
	paths []string,
) (string, error) {
	return commitWithPaths(ctx, treeRoot, []string{"commit", "--quiet", "--amend", "--no-edit"}, trailers, paths)
}

// commitWithPaths runs a `git commit` variant (baseArgs) scoped to exactly the
// given toplevel-relative paths, appending any trailers, then resolves and
// returns the resulting HEAD hash. Shared by CommitPaths and AmendPaths.
func commitWithPaths(
	ctx context.Context,
	treeRoot string,
	baseArgs []string,
	trailers []string,
	paths []string,
) (string, error) {
	args := make([]string, 0, 3+len(baseArgs)+2*len(trailers)+len(paths))
	args = append(args, "-C", treeRoot)
	args = append(args, baseArgs...)

	for _, trailer := range trailers {
		args = append(args, "--trailer", trailer)
	}

	args = append(args, "--")
	// the ":(top)" pathspec magic anchors each path to the repository
	// toplevel, matching the paths ChangedPaths reports even when treeRoot is
	// a subdirectory of the repository.
	for _, p := range paths {
		args = append(args, ":(top)"+p)
	}

	commitCmd := exec.CommandContext(ctx, "git", args...)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit failed: %w: %s", err, out)
	}

	revCmd := exec.CommandContext(ctx, "git", "-C", treeRoot, "rev-parse", "HEAD")

	out, err := revCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to resolve the created commit: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// HeadExists reports whether the repository has a commit at HEAD to amend (a
// freshly `git init`'d repo with no commits has none). Backed by
// `git rev-parse --verify --quiet HEAD`: exit 0 = HEAD resolves, a non-zero
// exit = no commit yet.
func HeadExists(ctx context.Context, treeRoot string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", treeRoot, "rev-parse", "--verify", "--quiet", "HEAD")

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}

		return false, fmt.Errorf("failed to check for a HEAD commit in %s: %w", treeRoot, err)
	}

	return true, nil
}

// HeadRemoteRefs returns the remote-tracking refs (e.g.
// "refs/remotes/origin/x") that contain HEAD — i.e. the remotes HEAD has been
// pushed to. A non-empty result means amending HEAD would rewrite
// already-published history. Backed by `git branch -r --contains HEAD`; this
// reads only LOCAL remote-tracking refs and does not fetch, so a HEAD pushed
// since the last fetch may not show up.
func HeadRemoteRefs(ctx context.Context, treeRoot string) ([]string, error) {
	cmd := exec.CommandContext(
		ctx, "git", "-C", treeRoot, "branch", "-r", "--contains", "HEAD", "--format=%(refname)",
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to check whether HEAD is pushed in %s: %w", treeRoot, err)
	}

	var refs []string

	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			refs = append(refs, line)
		}
	}

	return refs, nil
}
