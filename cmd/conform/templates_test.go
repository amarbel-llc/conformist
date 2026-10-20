package conform_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"code.linenisgreat.com/conformist/cmd/conform"
	"github.com/stretchr/testify/require"
)

func templatePath(id, name string) string {
	return filepath.Join("..", "..", "templates", id, name)
}

func readTemplateFile(t *testing.T, id, name string) string { //testui:allow // testify helper
	t.Helper()

	content, err := os.ReadFile(templatePath(id, name))
	require.NoError(t, err, "read templates/%s/%s", id, name)

	return string(content)
}

// TestRustTemplatesShareBoilerplate guards against drift between the sibling
// templates: the files that are language-independent must match templates/eng,
// and the files that are Rust-specific but package-independent must match across
// eng-rust, eng-rust-bin and eng-rust-lib. Each template is a deliberate copy of
// the others (no shared base yet), so this test is what keeps a fix to one from
// silently missing the rest.
func TestRustTemplatesShareBoilerplate(t *testing.T) {
	for _, name := range []string{".gitattributes", "sweatfile"} {
		want := readTemplateFile(t, "eng", name)

		for _, id := range []string{"eng-rust", "eng-rust-bin", "eng-rust-lib"} {
			require.Equal(t, want, readTemplateFile(t, id, name),
				"templates/%s/%s must match templates/eng/%s", id, name, name)
		}
	}

	for _, name := range []string{
		".gitignore", "conformist-impure.nix", "flake.nix", "justfile", "version.env",
	} {
		want := readTemplateFile(t, "eng-rust", name)

		for _, id := range []string{"eng-rust-bin", "eng-rust-lib"} {
			require.Equal(t, want, readTemplateFile(t, id, name),
				"templates/%s/%s must match templates/eng-rust/%s", id, name, name)
		}
	}
}

// TestGoTemplateSharesBoilerplate guards eng-go against drifting from templates/eng
// in the files that are language-independent.
func TestGoTemplateSharesBoilerplate(t *testing.T) {
	for _, name := range []string{".gitattributes", "sweatfile", "conformist-impure.nix", "version.env"} {
		require.Equal(t, readTemplateFile(t, "eng", name), readTemplateFile(t, "eng-go", name),
			"templates/eng-go/%s must match templates/eng/%s", name, name)
	}
}

// TestGoTemplateBootstrapsWithNoPlaceholderLeft runs the real bootstrap over the
// real templates/eng-go files and verifies that no EXAMPLE_ placeholder survives,
// that the module path, binary name and version key come out as the repo's, and
// that CLAUDE.md is a symlink to AGENTS.md (what the agents-md linter requires).
// The positive control is that the template does carry the placeholders first,
// so a clean result proves the substitution ran rather than that none existed.
func TestGoTemplateBootstrapsWithNoPlaceholderLeft(t *testing.T) {
	for _, name := range []string{"go.nix", "flake.nix", "main.go", "AGENTS.md"} {
		require.Contains(t, readTemplateFile(t, "eng-go", name), "EXAMPLE_NAME",
			"templates/eng-go/%s must carry the name placeholder", name)
	}

	for _, name := range []string{"version.env", "conformist.nix"} {
		require.Contains(t, readTemplateFile(t, "eng-go", name), "EXAMPLE_VERSION")
	}

	r, host := fixtureResolver(t, `{"data":[{"id":"eng-go","flakeref":"github:x#eng-go"}],"meta":{}}`)
	dir := t.TempDir()

	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver: r,
		Name:     "My-Juggler",
		NoLock:   true,
		FlakeInit: func(_ context.Context, d, _ string) error {
			src := filepath.Join("..", "..", "templates", "eng-go")

			return copyTemplateTree(src, d)
		},
	})
	require.NoError(t, err)

	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return walkErr
		}

		content, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotContains(t, string(content), "EXAMPLE_", "placeholder left in %s", path)

		return nil
	}))

	goNix, err := os.ReadFile(filepath.Join(dir, "go.nix"))
	require.NoError(t, err)
	require.Contains(t, string(goNix), `module = "code.linenisgreat.com/my-juggler";`)

	versionEnv, err := os.ReadFile(filepath.Join(dir, "version.env"))
	require.NoError(t, err)
	require.Contains(t, string(versionEnv), "export MY_JUGGLER_VERSION=0.1.0\n")

	target, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	require.NoError(t, err, "CLAUDE.md must be a symlink")
	require.Equal(t, "AGENTS.md", target)
}

// copyTemplateTree copies src into dst the way `nix flake init` does: regular
// files by content and mode, symlinks as symlinks.
func copyTemplateTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", path, err)
		}

		out := filepath.Join(dst, rel)

		switch {
		case entry.IsDir():
			return os.MkdirAll(out, 0o755)
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("reading link %s: %w", path, err)
			}

			return os.Symlink(target, out)
		default:
			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}

			return os.WriteFile(out, content, 0o644)
		}
	})
}

// TestTemplatesShipNoEnvrc verifies no template ships an .envrc: the fleet keeps
// it untracked and git-ignored (spinclass rewrites it in every worktree), so a
// tracked copy makes spinclass's merge dirty check fail (conformist#141).
func TestTemplatesShipNoEnvrc(t *testing.T) {
	for _, id := range []string{"eng", "eng-rust", "eng-rust-bin", "eng-rust-lib", "eng-go"} {
		_, err := os.Lstat(templatePath(id, ".envrc"))
		require.ErrorIs(t, err, os.ErrNotExist, "templates/%s must not ship an .envrc", id)
	}
}

// TestRustPackageTemplatesShipAgentsStub verifies the package templates carry the
// crate-name placeholder bootstrap substitutes, and that CLAUDE.md is a symlink to
// AGENTS.md (what the agents-md linter requires of a CLAUDE.md).
func TestRustPackageTemplatesShipAgentsStub(t *testing.T) {
	for _, id := range []string{"eng-rust-bin", "eng-rust-lib"} {
		require.Contains(t, readTemplateFile(t, id, "Cargo.toml"), `name = "EXAMPLE_CRATE"`,
			"templates/%s/Cargo.toml must carry the crate-name placeholder", id)
		require.Contains(t, readTemplateFile(t, id, "AGENTS.md"), "EXAMPLE_CRATE",
			"templates/%s/AGENTS.md must carry the crate-name placeholder", id)

		target, err := os.Readlink(templatePath(id, "CLAUDE.md"))
		require.NoError(t, err, "templates/%s/CLAUDE.md must be a symlink", id)
		require.Equal(t, "AGENTS.md", target)
	}
}
