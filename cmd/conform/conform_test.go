package conform_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/amarbel-llc/conformist/cmd/conform"
	"github.com/stretchr/testify/require"
)

// TestRunScaffoldsAbsentFiles verifies conform writes the scaffold files into an
// empty dir and reports the flake/justfile wiring.
func TestRunScaffoldsAbsentFiles(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer

	res, err := conform.Run(dir, &out)
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"conformist.nix", "version.env"}, res.Wrote)
	require.Empty(t, res.Skipped)

	for _, name := range res.Wrote {
		_, statErr := os.Stat(filepath.Join(dir, name))
		require.NoError(t, statErr, "scaffold %s should exist on disk", name)
	}

	report := out.String()
	require.Contains(t, report, "conformist.lib.presets.eng", "report must show the preset wiring")
	require.Contains(t, report, "lint-fmt", "report must show the justfile recipes")
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
	require.ElementsMatch(t, []string{"conformist.nix", "version.env"}, res.Skipped)
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

	require.Equal(t, []string{"version.env"}, res.Wrote)
	require.Equal(t, []string{"conformist.nix"}, res.Skipped)

	got, err := os.ReadFile(existing)
	require.NoError(t, err)
	require.Equal(t, sentinel, string(got), "existing conformist.nix must be untouched")
}
