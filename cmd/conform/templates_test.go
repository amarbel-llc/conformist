package conform_test

import (
	"os"
	"path/filepath"
	"testing"

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

// TestTemplatesShipNoEnvrc verifies no template ships an .envrc: the fleet keeps
// it untracked and git-ignored (spinclass rewrites it in every worktree), so a
// tracked copy makes spinclass's merge dirty check fail (conformist#141).
func TestTemplatesShipNoEnvrc(t *testing.T) {
	for _, id := range []string{"eng", "eng-rust", "eng-rust-bin", "eng-rust-lib"} {
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
