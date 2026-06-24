package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const TreeRootCmd = "git rev-parse --show-toplevel"

// OriginRepoName returns the repository name parsed from the `origin` remote
// URL of the git repo at dir — the last path segment with any trailing `.git`
// stripped. For example both `git@github.com:amarbel-llc/conformist.git` and
// `https://github.com/amarbel-llc/conformist` yield "conformist".
//
// It returns ("", nil) — not an error — when dir is not a git repo or has no
// `origin` remote, so a caller can fall back to another source (e.g. the
// directory basename). A non-nil error is reserved for an unexpected failure to
// run git at all (e.g. git not on PATH).
func OriginRepoName(dir string) (string, error) {
	cmd := exec.CommandContext(
		context.Background(), "git", "-C", dir, "config", "--get", "remote.origin.url",
	)

	out, err := cmd.Output()
	if err != nil {
		// `git config --get` exits non-zero when the key is absent, and
		// invocations outside a repo exit non-zero too. Both arrive as
		// ExitError; treat them as "no origin remote" so the caller falls back
		// rather than failing the scaffold.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil
		}

		return "", fmt.Errorf("failed to read origin remote in %s: %w", dir, err)
	}

	return repoNameFromRemoteURL(string(out)), nil
}

// repoNameFromRemoteURL extracts the repository name from a git remote URL: the
// final `/`- or `:`-delimited segment, with a trailing slash and any trailing
// `.git` removed. Returns "" when no name can be recovered.
func repoNameFromRemoteURL(raw string) string {
	url := strings.TrimSpace(raw)
	url = strings.TrimSuffix(url, "/")
	url = strings.TrimSuffix(url, ".git")

	// scp-like (git@host:owner/repo) and URL (https://host/owner/repo) forms
	// both end in the repo name after the last '/' or ':'.
	if idx := strings.LastIndexAny(url, "/:"); idx >= 0 {
		url = url[idx+1:]
	}

	return url
}

func IsInsideWorktree(path string) (bool, error) {
	// check if the root is a git repository
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = path

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.Contains(string(exitErr.Stderr), "not a git repository") {
			return false, nil
		}

		return false, fmt.Errorf("failed to check if %s is a git repository: %w", path, err)
	}

	if strings.Trim(string(out), "\n") != "true" {
		// not a git repo
		return false, nil
	}

	// is a git repo
	return true, nil
}
