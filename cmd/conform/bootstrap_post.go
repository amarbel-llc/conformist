package conform

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"code.linenisgreat.com/conformist/git"
)

// versionKeyPlaceholder is the literal the eng templates ship in version.env and
// conformist.nix; bootstrap replaces it with the repo's <REPO>_VERSION key.
const versionKeyPlaceholder = "EXAMPLE_VERSION"

// keyedFiles are the template files whose versionKeyPlaceholder is replaced.
var keyedFiles = []string{"version.env", "conformist.nix"}

// treeSnapshot maps each non-.git path under a dir (slash-separated, relative) to
// a digest of its content (a symlink hashes its target), so bootstrap can tell
// which files `nix flake init` wrote or changed.
type treeSnapshot map[string][sha256.Size]byte

func snapshotTree(dir string) (treeSnapshot, error) {
	snap := treeSnapshot{}

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		if entry.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", path, err)
		}

		var content []byte
		if entry.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("reading link %s: %w", path, err)
			}

			content = []byte("symlink:" + target)
		} else {
			content, err = os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
		}

		snap[filepath.ToSlash(rel)] = sha256.Sum256(content)

		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return snap, nil
	}

	if err != nil {
		return nil, fmt.Errorf("snapshotting %s: %w", dir, err)
	}

	return snap, nil
}

// touchedSince returns the sorted paths that are new or changed relative to
// before: exactly the files the template wrote, never a pre-existing user file
// left alone by --overwrite.
func (after treeSnapshot) touchedSince(before treeSnapshot) []string {
	var touched []string

	for path, digest := range after {
		if prev, ok := before[path]; !ok || prev != digest {
			touched = append(touched, path)
		}
	}

	sort.Strings(touched)

	return touched
}

// applyVersionKey replaces versionKeyPlaceholder with key+"_VERSION" in the
// keyed files the template wrote, and returns the files it rewrote. A template
// without the placeholder (or a file the user already had) is left untouched.
func applyVersionKey(dir, key string, touched []string) ([]string, error) {
	wasTouched := map[string]bool{}
	for _, path := range touched {
		wasTouched[path] = true
	}

	var rewrote []string

	for _, name := range keyedFiles {
		if !wasTouched[name] {
			continue
		}

		path := filepath.Join(dir, name)

		content, err := os.ReadFile(path)
		if err != nil {
			return rewrote, fmt.Errorf("reading %s: %w", name, err)
		}

		if !strings.Contains(string(content), versionKeyPlaceholder) {
			continue
		}

		replaced := strings.ReplaceAll(string(content), versionKeyPlaceholder, key+"_VERSION")
		if err := os.WriteFile(path, []byte(replaced), 0o644); err != nil { //nolint:gosec // template file, not a secret
			return rewrote, fmt.Errorf("writing %s: %w", name, err)
		}

		rewrote = append(rewrote, name)
	}

	return rewrote, nil
}

// bootstrapVersionKey is the key prefix bootstrap substitutes: an explicit name
// when given, else the same origin-remote-then-directory derivation local-mode
// conform uses.
func bootstrapVersionKey(dir, name string) string {
	if name != "" {
		return normalizeVersionKey(name)
	}

	return projectVersionKey(dir)
}

// isGitRepo reports whether dir is a git work tree root (a .git directory, or a
// .git file as in a linked worktree).
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))

	return err == nil
}

// runGitStage stages exactly paths (relative to dir) — never a commit.
func runGitStage(ctx context.Context, dir string, paths []string) error {
	args := append([]string{"-C", dir, "add", "--"}, paths...)

	out, err := exec.CommandContext(ctx, git.Binary, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// runFlakeLock runs `nix flake lock` in dir, inheriting stderr so nix's progress
// and trust prompts reach the operator.
func runFlakeLock(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "nix", "flake", "lock")
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	return cmd.Run() //nolint:wrapcheck // caller wraps with context
}
