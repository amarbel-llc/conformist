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
	for _, name := range []string{"go.nix", "flake.nix", "main.go", "AGENTS.md", "doc/cli.1.scd"} {
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

	mainGo, err := os.ReadFile(filepath.Join(dir, "main.go"))
	require.NoError(t, err)
	require.Contains(t, string(mainGo), `"code.linenisgreat.com/my-juggler/internal/0/buildinfo"`,
		"bootstrap must repoint the internal import at the new module")
	require.NotContains(t, string(mainGo), "code.linenisgreat.com/conformist/")

	manpage, err := os.ReadFile(filepath.Join(dir, "doc", "cli.1.scd"))
	require.NoError(t, err)
	require.Contains(t, string(manpage), "my-juggler - ")

	versionEnv, err := os.ReadFile(filepath.Join(dir, "version.env"))
	require.NoError(t, err)
	require.Contains(t, string(versionEnv), "export MY_JUGGLER_VERSION=0.1.0\n")

	target, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	require.NoError(t, err, "CLAUDE.md must be a symlink")
	require.Equal(t, "AGENTS.md", target)
}

// TestGoTemplateShipsLayoutAndManpageLanes pins conformist#143: the Go template
// carries a dagnabit package-layout lane and a section-1 manpage with the
// NAME-line lint, each wired into the lint aggregate the pre-merge gate runs.
// The internal/0 package is a real example (main.go uses it, it has a test) so
// the layout lane checks a package rather than passing vacuously.
func TestGoTemplateShipsLayoutAndManpageLanes(t *testing.T) {
	flake := readTemplateFile(t, "eng-go", "flake.nix")
	for _, want := range []string{
		"purse-first.url", "doppelgang.url",
		"conformist.lib.mkLayoutCheck", "purse-first.packages.${system}.dagnabit",
		"doppelgang lint-man", "checks.layout", "checks.manpage-names",
		"packages.manpages",
	} {
		require.Contains(t, flake, want, "templates/eng-go/flake.nix must wire %q", want)
	}

	// The layout command lives in conformist.lib.mkLayoutCheck (conformist#32),
	// not as an inline copy per repo.
	require.NotContains(t, flake, "dagnabit -n", "templates/eng-go/flake.nix must use mkLayoutCheck, not an inline copy")

	// Both new inputs follow the template's own pins; none floats on its own.
	for _, input := range []string{"purse-first", "doppelgang"} {
		for _, follows := range []string{"igloo", "utils", "conformist", "nixpkgs-master"} {
			require.Contains(t, flake, input+".inputs."+follows+".follows",
				"templates/eng-go/flake.nix: %s must follow %s", input, follows)
		}
	}

	justfile := readTemplateFile(t, "eng-go", "justfile")
	require.Regexp(t, `(?m)^lint: .*\blint-layout\b.*\blint-manpage-names\b`, justfile,
		"templates/eng-go/justfile: lint must aggregate both new lanes")
	require.Contains(t, justfile, "\nlint-layout:")
	require.Contains(t, justfile, "\nlint-manpage-names:")

	page := readTemplateFile(t, "eng-go", "doc/cli.1.scd")
	require.Regexp(t, `(?m)^EXAMPLE_NAME\(1\)`, page, "scdoc header names the binary")
	require.Regexp(t, `(?m)^# NAME\n\nEXAMPLE_NAME - \S`, page, "NAME line: `name - summary`")

	require.Contains(t, readTemplateFile(t, "eng-go", "main.go"), "/internal/0/",
		"main.go must import the example level-0 package")

	entries, err := os.ReadDir(filepath.Join(templatePath("eng-go", "internal"), "0"))
	require.NoError(t, err, "templates/eng-go/internal/0 must exist")
	require.Len(t, entries, 1, "one example leaf package")

	leaf := filepath.Join("internal", "0", entries[0].Name())
	_, err = os.Stat(templatePath("eng-go", filepath.Join(leaf, entries[0].Name()+"_test.go")))
	require.NoError(t, err, "the example package carries a test")

	agents := readTemplateFile(t, "eng-go", "AGENTS.md")
	require.Contains(t, agents, "alfa", "AGENTS.md documents how to add a level")
	require.Contains(t, agents, "lint-layout")
	require.Contains(t, agents, "lint-manpage-names")
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

// TestLintWorktreeRunsThePinnedConformist pins conformist#131: every generated
// lint-worktree recipe runs the flake's own pinned binary (`nix run
// .#conformist`), never a bare `conformist` that PATH may resolve to eng's
// home-profile wrapper, and every generated flake exports that package. The
// scaffold files are read from disk; they are //go:embed-ed by the package.
func TestLintWorktreeRunsThePinnedConformist(t *testing.T) {
	const pinned = `nix run '.#conformist' -- check --config-file "$cfg" --tree-root .`

	read := func(path string) string { //testui:allow // testify helper
		t.Helper()

		content, err := os.ReadFile(path)
		require.NoError(t, err, path)

		return string(content)
	}

	recipes := map[string]string{"scaffold/justfile": read("scaffold/justfile"), "scaffold/recipes.just": read("scaffold/recipes.just")}
	flakes := map[string]string{"scaffold/flake.nix": read("scaffold/flake.nix"), "scaffold/flake-snippet.txt": read("scaffold/flake-snippet.txt")}

	for _, id := range []string{"eng", "eng-go", "eng-rust", "eng-rust-bin", "eng-rust-lib"} {
		recipes["templates/"+id+"/justfile"] = readTemplateFile(t, id, "justfile")
		flakes["templates/"+id+"/flake.nix"] = readTemplateFile(t, id, "flake.nix")
	}

	for name, content := range recipes {
		require.Contains(t, content, pinned, "%s: lint-worktree must run the pinned conformist", name)
		require.NotRegexp(t, `(?m)^\s+conformist check`, content, "%s: bare conformist on PATH", name)
	}

	for name, content := range flakes {
		require.Contains(t, content, "packages.conformist = conformistPkg;", "%s must export the pinned conformist", name)
	}
}

// TestRustTemplatesShipManpageLane pins the Rust half of conformist#143: the
// shared flake builds doc/*.scd when a template ships them and lints the NAME
// lines, and the shared justfile aggregates the lane. The package templates ship
// the page (cli.1 for the binary, lib.7 for the library) carrying the crate-name
// placeholder; the page-less base ships none.
func TestRustTemplatesShipManpageLane(t *testing.T) {
	for _, id := range []string{"eng-rust", "eng-rust-bin", "eng-rust-lib"} {
		flake := readTemplateFile(t, id, "flake.nix")
		for _, want := range []string{"doppelgang.url", "doppelgang lint-man", "checks.manpage-names", "packages.manpages"} {
			require.Contains(t, flake, want, "templates/%s/flake.nix must wire %q", id, want)
		}

		for _, follows := range []string{"utils", "conformist", "nixpkgs-master"} {
			require.Contains(t, flake, "doppelgang.inputs."+follows+".follows", "templates/%s: doppelgang must follow %s", id, follows)
		}

		justfile := readTemplateFile(t, id, "justfile")
		require.Regexp(t, `(?m)^lint: .*\blint-manpage-names\b`, justfile)
		require.Contains(t, justfile, "\nlint-manpage-names:")
	}

	for id, page := range map[string]string{"eng-rust-bin": "cli.1.scd", "eng-rust-lib": "lib.7.scd"} {
		content := readTemplateFile(t, id, filepath.Join("doc", page))
		require.Regexp(t, `(?m)^# NAME\n\nEXAMPLE_CRATE - \S`, content, "templates/%s/doc/%s: NAME line", id, page)
		require.Contains(t, readTemplateFile(t, id, "AGENTS.md"), "lint-manpage-names")
	}

	_, err := os.Lstat(templatePath("eng-rust", "doc"))
	require.ErrorIs(t, err, os.ErrNotExist, "the page-less base ships no doc/")
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
