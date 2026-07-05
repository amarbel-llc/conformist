package conform_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/amarbel-llc/conformist/cmd/conform"
	"github.com/stretchr/testify/require"
)

// allScaffolds is every file conform writes into a greenfield repo.
var allScaffolds = []string{"conformist.nix", "version.env", "sweatfile", "flake.nix", "justfile"}

// TestRunScaffoldsGreenfield verifies conform writes the full eng shape —
// including complete, buildable flake.nix and justfile — into an empty dir, and
// does not fall back to printing the paste-in snippets (there is nothing to edit).
func TestRunScaffoldsGreenfield(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer

	res, err := conform.Run(dir, &out, conform.Options{})
	require.NoError(t, err)

	require.ElementsMatch(t, allScaffolds, res.Wrote)
	require.Empty(t, res.Skipped)

	for _, name := range res.Wrote {
		_, statErr := os.Stat(filepath.Join(dir, name))
		require.NoError(t, statErr, "scaffold %s should exist on disk", name)
	}

	flake, err := os.ReadFile(filepath.Join(dir, "flake.nix"))
	require.NoError(t, err)
	require.Contains(t, string(flake), "utils.lib.eachDefaultSystem",
		"greenfield flake.nix must be the complete file, not the wiring snippet")

	just, err := os.ReadFile(filepath.Join(dir, "justfile"))
	require.NoError(t, err)
	require.Contains(t, string(just), "default: lint",
		"greenfield justfile must be the complete file, not the recipe snippet")

	report := out.String()
	require.NotContains(t, report, "add to flake.nix",
		"a freshly-written flake.nix must not also print the paste-in snippet")
	require.NotContains(t, report, "add to justfile",
		"a freshly-written justfile must not also print the recipe snippet")
}

// TestRunIsIdempotent verifies a second run writes nothing and reports the
// existing files as kept — conform never clobbers an adopted repo.
func TestRunIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	_, err := conform.Run(dir, &bytes.Buffer{}, conform.Options{})
	require.NoError(t, err)

	res, err := conform.Run(dir, &bytes.Buffer{}, conform.Options{})
	require.NoError(t, err)

	require.Empty(t, res.Wrote, "second run must not rewrite existing files")
	require.ElementsMatch(t, allScaffolds, res.Skipped)
}

// TestRunReportsExistingFlakeAndJustfile verifies the fallback path: when
// flake.nix is not the recognized eachDefaultSystem shape (and justfile, which
// is never edited, exists), conform leaves them untouched and prints the wiring
// snippets to paste, while still writing the absent conformist.nix / version.env.
func TestRunReportsExistingFlakeAndJustfile(t *testing.T) {
	dir := t.TempDir()

	const flakeSentinel = "# my own flake.nix\n"
	const justSentinel = "# my own justfile\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(flakeSentinel), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "justfile"), []byte(justSentinel), 0o644))

	var out bytes.Buffer

	res, err := conform.Run(dir, &out, conform.Options{})
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"conformist.nix", "version.env", "sweatfile"}, res.Wrote)
	require.ElementsMatch(t, []string{"flake.nix", "justfile"}, res.Skipped)

	gotFlake, err := os.ReadFile(filepath.Join(dir, "flake.nix"))
	require.NoError(t, err)
	require.Equal(t, flakeSentinel, string(gotFlake), "existing flake.nix must be untouched")

	gotJust, err := os.ReadFile(filepath.Join(dir, "justfile"))
	require.NoError(t, err)
	require.Equal(t, justSentinel, string(gotJust), "existing justfile must be untouched")

	report := out.String()
	require.Contains(t, report, "add to flake.nix", "report must show the flake.nix wiring to paste")
	require.Contains(t, report, "conformist.lib.presets.eng", "report must show the preset wiring")
	require.Contains(t, report, "add to justfile", "report must show the justfile wiring to paste")
	require.Contains(t, report, "lint-fmt", "report must show the justfile recipes")
}

// TestRunPreservesExistingFiles verifies conform leaves a pre-existing file's
// content untouched (it scaffolds only what is absent).
func TestRunPreservesExistingFiles(t *testing.T) {
	dir := t.TempDir()

	const sentinel = "# my own conformist.nix\n"
	existing := filepath.Join(dir, "conformist.nix")
	require.NoError(t, os.WriteFile(existing, []byte(sentinel), 0o644))

	res, err := conform.Run(dir, &bytes.Buffer{}, conform.Options{})
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"version.env", "sweatfile", "flake.nix", "justfile"}, res.Wrote)
	require.Equal(t, []string{"conformist.nix"}, res.Skipped)

	got, err := os.ReadFile(existing)
	require.NoError(t, err)
	require.Equal(t, sentinel, string(got), "existing conformist.nix must be untouched")
}

// recognizedFlake is a brownfield flake in the eachDefaultSystem shape that
// does not yet reference conformist — the in-place editing target.
const recognizedFlake = `{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages.default = pkgs.hello;
      }
    );
}
`

// TestRunEditsRecognizedFlake verifies the in-place path: a recognized
// flake.nix is edited (not printed), and the conformist wiring lands in the
// file. The edit counts as a tree change (exit 3).
func TestRunEditsRecognizedFlake(t *testing.T) {
	dir := t.TempDir()
	flakePath := filepath.Join(dir, "flake.nix")
	require.NoError(t, os.WriteFile(flakePath, []byte(recognizedFlake), 0o644))

	var out bytes.Buffer
	res, err := conform.Run(dir, &out, conform.Options{})
	require.NoError(t, err)

	require.NotEmpty(t, res.Edited, "a recognized flake.nix must be edited in place")
	require.True(t, res.Changed())
	require.NotContains(t, res.Skipped, "flake.nix", "an edited flake.nix is not 'kept'")

	got, err := os.ReadFile(flakePath)
	require.NoError(t, err)
	require.Contains(t, string(got), `conformist.url = "github:amarbel-llc/conformist";`)
	require.Contains(t, string(got), "eval = conformist.lib.evalModule pkgs {")
	require.Contains(t, string(got), "packages.default = pkgs.hello;", "the repo's own output is preserved")

	report := out.String()
	require.Contains(t, report, "edited flake.nix")
	require.NotContains(t, report, "add to flake.nix", "an edited flake.nix must not also print the snippet")
}

// TestRunNoEditPrintsSnippet verifies --no-edit leaves a recognized flake.nix
// untouched and prints the wiring instead.
func TestRunNoEditPrintsSnippet(t *testing.T) {
	dir := t.TempDir()
	flakePath := filepath.Join(dir, "flake.nix")
	require.NoError(t, os.WriteFile(flakePath, []byte(recognizedFlake), 0o644))

	var out bytes.Buffer
	res, err := conform.Run(dir, &out, conform.Options{NoEdit: true})
	require.NoError(t, err)

	require.Empty(t, res.Edited, "--no-edit must not edit flake.nix")
	require.Contains(t, res.Skipped, "flake.nix")

	got, err := os.ReadFile(flakePath)
	require.NoError(t, err)
	require.Equal(t, recognizedFlake, string(got), "--no-edit must leave flake.nix byte-identical")

	require.Contains(t, out.String(), "add to flake.nix", "--no-edit must print the wiring to paste")
}

// TestVersionEnvKeyFromDirName verifies version.env's key is derived from the
// repo name (here the directory basename, with git's upward search fenced).
func TestVersionEnvKeyFromDirName(t *testing.T) {
	parent := t.TempDir()
	// Fence git so OriginRepoName cannot resolve the enclosing worktree's
	// origin ($TMPDIR lives inside it), forcing the directory-basename path.
	t.Setenv("GIT_CEILING_DIRECTORIES", parent)
	dir := filepath.Join(parent, "my-repo")
	require.NoError(t, os.Mkdir(dir, 0o755))

	_, err := conform.Run(dir, &bytes.Buffer{}, conform.Options{})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "version.env"))
	require.NoError(t, err)
	require.Contains(t, string(got), "MY_REPO_VERSION=", "key derives from the repo dir name")
	require.NotContains(t, string(got), "EXAMPLE_VERSION", "the EXAMPLE placeholder must be replaced")
}

// TestRunEmitsRepairCommand verifies the default (no --repair) run prints the
// single working-tree repair command that delegates to conformist's own linters
// (#42(ii)), and points at `conform --repair`.
func TestRunEmitsRepairCommand(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer
	_, err := conform.Run(dir, &out, conform.Options{})
	require.NoError(t, err)

	report := out.String()
	require.Contains(t, report, conform.RepairCommand, "the report emits the exact repair command")
	require.Contains(t, report, "conform --repair", "the report points at the inline repair path")
}

// TestRunRepairInvokesRunner verifies --repair runs RepairCommand via the
// injected runner and that a tree-changing repair marks the result changed
// (exit 3), while a no-op repair leaves it unchanged (exit 0).
func TestRunRepairInvokesRunner(t *testing.T) {
	t.Run("changed", func(t *testing.T) {
		dir := t.TempDir()
		// Pre-create every scaffold so the run writes nothing; only repair can
		// change the tree here.
		seedAllScaffolds(t, dir)

		var gotCmd string
		var out bytes.Buffer
		res, err := conform.Run(dir, &out, conform.Options{
			Repair: true,
			RepairRunner: func(_, command string) (bool, error) {
				gotCmd = command

				return true, nil
			},
		})
		require.NoError(t, err)
		require.Equal(t, conform.RepairCommand, gotCmd, "--repair runs the exact emitted command")
		require.True(t, res.Repaired)
		require.True(t, res.Changed(), "a tree-changing repair is a changed (exit 3) outcome")
		require.NotContains(t, out.String(), conform.RepairCommand, "having run repair, it does not also print the command")
	})

	t.Run("noop", func(t *testing.T) {
		dir := t.TempDir()
		seedAllScaffolds(t, dir)

		res, err := conform.Run(dir, &bytes.Buffer{}, conform.Options{
			Repair:       true,
			RepairRunner: func(_, _ string) (bool, error) { return false, nil },
		})
		require.NoError(t, err)
		require.False(t, res.Repaired)
		require.False(t, res.Changed(), "an already-conformant tree is an exit-0 outcome")
	})
}

// TestRunSilentWhenJustfileAlreadyWired verifies #42 conformance detection: an
// existing justfile that already carries conformist's lint recipes is not
// nagged with the paste snippet.
func TestRunSilentWhenJustfileAlreadyWired(t *testing.T) {
	dir := t.TempDir()
	wiredJust := "default: lint\n\nlint: lint-fmt\n\nlint-fmt:\n    nix build \".#checks.${system}.formatting\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "justfile"), []byte(wiredJust), 0o644))

	var out bytes.Buffer
	res, err := conform.Run(dir, &out, conform.Options{})
	require.NoError(t, err)

	require.Contains(t, res.Skipped, "justfile")
	require.NotContains(t, out.String(), "add to justfile",
		"a justfile already carrying the conformist recipes must not be nagged")
}

// TestRunSilentWhenFlakeHandWired verifies #42 conformance detection for an
// unrecognized-shape flake (e.g. flake-parts) that a user already wired by hand:
// conform leaves it silent instead of printing the paste snippet.
func TestRunSilentWhenFlakeHandWired(t *testing.T) {
	dir := t.TempDir()
	// Not the eachDefaultSystem shape flakeedit recognizes, but it already
	// references conformist's module wiring.
	handWired := "{\n  inputs.conformist.url = \"github:amarbel-llc/conformist\";\n  outputs = inputs: " +
		"inputs.flake-parts.lib.mkFlake { inherit inputs; } {\n" +
		"    perSystem = { pkgs, ... }: {\n      formatter = (inputs.conformist.lib.evalModule pkgs {}).config.build.wrapper;\n" +
		"    };\n  };\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(handWired), 0o644))

	var out bytes.Buffer
	res, err := conform.Run(dir, &out, conform.Options{})
	require.NoError(t, err)

	require.Contains(t, res.Skipped, "flake.nix")
	require.Empty(t, res.Edited, "an unrecognized shape is not edited in place")
	require.NotContains(t, out.String(), "add to flake.nix",
		"a hand-wired flake must not be nagged with the paste snippet")
}

// seedAllScaffolds writes every scaffold file conform would create, so a
// subsequent Run writes nothing and only the repair step can change the result.
func seedAllScaffolds(t *testing.T, dir string) { //testui:allow // testify helper
	t.Helper()
	_, err := conform.Run(dir, &bytes.Buffer{}, conform.Options{})
	require.NoError(t, err)
}

// TestScaffoldFlakeAndJustfileMatchTemplate guards against drift: the full-file
// scaffolds conform embeds must stay byte-identical to the canonical eng
// template files (templates/eng), so `conformist conform` and
// `nix flake init -t …#eng` produce the same eng shape.
func TestScaffoldFlakeAndJustfileMatchTemplate(t *testing.T) {
	for _, name := range []string{"flake.nix", "justfile", "sweatfile"} {
		embedded, err := os.ReadFile(filepath.Join("scaffold", name))
		require.NoError(t, err, "read embedded scaffold/%s", name)

		template, err := os.ReadFile(filepath.Join("..", "..", "templates", "eng", name))
		require.NoError(t, err, "read templates/eng/%s", name)

		require.Equal(t, string(template), string(embedded),
			"scaffold/%s must match templates/eng/%s — keep them in sync", name, name)
	}
}
