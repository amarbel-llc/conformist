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
var allScaffolds = []string{"conformist.nix", "version.env", "flake.nix", "justfile"}

// TestRunScaffoldsGreenfield verifies conform writes the full eng shape —
// including complete, buildable flake.nix and justfile — into an empty dir, and
// does not fall back to printing the paste-in snippets (there is nothing to edit).
func TestRunScaffoldsGreenfield(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer

	res, err := conform.Run(dir, &out)
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

	_, err := conform.Run(dir, &bytes.Buffer{})
	require.NoError(t, err)

	res, err := conform.Run(dir, &bytes.Buffer{})
	require.NoError(t, err)

	require.Empty(t, res.Wrote, "second run must not rewrite existing files")
	require.ElementsMatch(t, allScaffolds, res.Skipped)
}

// TestRunReportsExistingFlakeAndJustfile verifies the brownfield path: when
// flake.nix and justfile already exist, conform leaves them untouched (editing
// an existing flake.nix is the fragile part) and prints the wiring snippets to
// paste, while still writing the absent conformist.nix / version.env.
func TestRunReportsExistingFlakeAndJustfile(t *testing.T) {
	dir := t.TempDir()

	const flakeSentinel = "# my own flake.nix\n"
	const justSentinel = "# my own justfile\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(flakeSentinel), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "justfile"), []byte(justSentinel), 0o644))

	var out bytes.Buffer

	res, err := conform.Run(dir, &out)
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"conformist.nix", "version.env"}, res.Wrote)
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

	res, err := conform.Run(dir, &bytes.Buffer{})
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"version.env", "flake.nix", "justfile"}, res.Wrote)
	require.Equal(t, []string{"conformist.nix"}, res.Skipped)

	got, err := os.ReadFile(existing)
	require.NoError(t, err)
	require.Equal(t, sentinel, string(got), "existing conformist.nix must be untouched")
}

// TestScaffoldFlakeAndJustfileMatchTemplate guards against drift: the full-file
// scaffolds conform embeds must stay byte-identical to the canonical eng
// template files (templates/eng), so `conformist conform` and
// `nix flake init -t …#eng` produce the same eng shape.
func TestScaffoldFlakeAndJustfileMatchTemplate(t *testing.T) {
	for _, name := range []string{"flake.nix", "justfile"} {
		embedded, err := os.ReadFile(filepath.Join("scaffold", name))
		require.NoError(t, err, "read embedded scaffold/%s", name)

		template, err := os.ReadFile(filepath.Join("..", "..", "templates", "eng", name))
		require.NoError(t, err, "read templates/eng/%s", name)

		require.Equal(t, string(template), string(embedded),
			"scaffold/%s must match templates/eng/%s — keep them in sync", name, name)
	}
}
