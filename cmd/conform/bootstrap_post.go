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

// crateNamePlaceholder is the literal the Rust package templates ship as the
// Cargo package name (and in their AGENTS.md); bootstrap replaces it with the
// repo's crate name.
const crateNamePlaceholder = "EXAMPLE_CRATE"

// crateNamedFiles are the template files whose crateNamePlaceholder is replaced.
var crateNamedFiles = []string{"Cargo.toml", "AGENTS.md"}

// goNamePlaceholder is the literal the Go template ships as the module path's
// last element, the package name and the binary's name; bootstrap replaces it
// with the repo's lower-cased name.
const goNamePlaceholder = "EXAMPLE_NAME"

// goNamedFiles are the template files whose goNamePlaceholder is replaced.
var goNamedFiles = []string{"go.nix", "flake.nix", "main.go", "AGENTS.md"}

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

// substitution replaces token with value in the named files, when the template
// wrote them.
type substitution struct {
	token string
	value string
	files []string
}

// applySubstitutions applies each substitution to the files the template wrote
// and returns, per substitution token, the files it rewrote. A template without
// the token (or a file the user already had) is left untouched.
func applySubstitutions(dir string, touched []string, subs []substitution) (map[string][]string, error) {
	wasTouched := map[string]bool{}
	for _, path := range touched {
		wasTouched[path] = true
	}

	rewrote := map[string][]string{}

	for _, sub := range subs {
		for _, name := range sub.files {
			if !wasTouched[name] {
				continue
			}

			path := filepath.Join(dir, name)

			content, err := os.ReadFile(path)
			if err != nil {
				return rewrote, fmt.Errorf("reading %s: %w", name, err)
			}

			if !strings.Contains(string(content), sub.token) {
				continue
			}

			replaced := strings.ReplaceAll(string(content), sub.token, sub.value)
			if err := os.WriteFile(path, []byte(replaced), 0o644); err != nil { //nolint:gosec // template file, not a secret
				return rewrote, fmt.Errorf("writing %s: %w", name, err)
			}

			rewrote[sub.token] = append(rewrote[sub.token], name)
		}
	}

	return rewrote, nil
}

// repoName is the repository's name: the git origin remote's repo name, falling
// back to the directory basename.
func repoName(dir string) string {
	name, err := git.OriginRepoName(dir)
	if err != nil || name == "" {
		name = filepath.Base(dir)
	}

	return name
}

// bootstrapSubstitutions are the placeholders every bootstrapped template may
// carry, derived from an explicit name when given, else from repoName: the
// <REPO>_VERSION key and a Cargo-legal crate name (also the Go template's name,
// being a valid lower-case Go module path element).
func bootstrapSubstitutions(dir, name string) []substitution {
	if name == "" {
		name = repoName(dir)
	}

	return []substitution{
		{versionKeyPlaceholder, normalizeVersionKey(name) + "_VERSION", keyedFiles},
		{crateNamePlaceholder, normalizeCrateName(name), crateNamedFiles},
		{goNamePlaceholder, normalizeCrateName(name), goNamedFiles},
	}
}

// normalizeCrateName lower-cases name and replaces every rune Cargo does not
// allow in a package name ([a-z0-9_-]) with '-'. A name that does not start with
// a letter gets a "crate-" prefix, since Cargo requires one.
func normalizeCrateName(name string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}

	crate := strings.Trim(b.String(), "-_")
	if crate == "" || crate[0] < 'a' || crate[0] > 'z' {
		crate = "crate-" + crate
	}

	return strings.TrimSuffix(crate, "-")
}

// appendUnique appends item to list unless it is already there.
func appendUnique(list []string, item string) []string {
	for _, existing := range list {
		if existing == item {
			return list
		}
	}

	return append(list, item)
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
