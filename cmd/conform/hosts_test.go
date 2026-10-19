package conform_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/cmd/conform"
	"github.com/stretchr/testify/require"
)

// gitInitWithOrigin makes dir a git repo whose origin remote is url.
func gitInitWithOrigin(t *testing.T, dir, url string) { //testui:allow // testify helper
	t.Helper()

	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", url},
	} {
		out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
}

func readFile(t *testing.T, path string) string { //testui:allow // testify helper
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(content)
}

const managedHostsLine = `linters.git-remotes.allowed-hosts = [ "forge.example.org" ]; ` +
	"# managed by `conformist conform` (--allow-host)"

// TestRunPersistsOriginHostAndFlag verifies local mode writes the allowlist into
// the overlay: the non-canonical origin host automatically, plus --allow-host.
func TestRunPersistsOriginHostAndFlag(t *testing.T) {
	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@forge.example.org:sasha/prophit.git")

	var out bytes.Buffer
	_, err := conform.Run(dir, &out, conform.Options{AllowHosts: []string{"GitHub.com"}})
	require.NoError(t, err)

	overlay := readFile(t, filepath.Join(dir, "conformist-impure.nix"))
	require.Contains(t, overlay,
		`linters.git-remotes.allowed-hosts = [ "github.com" "forge.example.org" ]; # managed by `)
	require.Contains(t, out.String(), "allowed git-remotes host(s): github.com, forge.example.org")
	require.NotContains(t, out.String(), "note:", "the greenfield flake imports the overlay, so no advice is needed")
}

// TestRunCanonicalOriginPersistsNothing verifies an origin on the canonical host
// leaves the overlay a plain stub.
func TestRunCanonicalOriginPersistsNothing(t *testing.T) {
	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@code.linenisgreat.com:conformist.git")

	_, err := conform.Run(dir, &bytes.Buffer{}, conform.Options{})
	require.NoError(t, err)

	require.NotContains(t, readFile(t, filepath.Join(dir, "conformist-impure.nix")), "\n  linters.git-remotes.allowed-hosts",
		"no code line is added (the stub's comments mention the option, so match an uncommented attribute)")
}

// TestRunRerunKeepsAndWidensHosts verifies a re-run without the flag keeps the
// persisted host (the origin still names it) and a later flag widens, never
// narrows, the managed line.
func TestRunRerunKeepsAndWidensHosts(t *testing.T) {
	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@forge.example.org:sasha/prophit.git")

	_, err := conform.Run(dir, &bytes.Buffer{}, conform.Options{})
	require.NoError(t, err)

	first := readFile(t, filepath.Join(dir, "conformist-impure.nix"))
	require.Contains(t, first, managedHostsLine)

	// Origin moved back to canonical, no flag: the persisted host stays.
	out, err := exec.CommandContext(context.Background(), "git", "-C", dir,
		"remote", "set-url", "origin", "git@code.linenisgreat.com:prophit.git").CombinedOutput()
	require.NoErrorf(t, err, "%s", out)

	_, err = conform.Run(dir, &bytes.Buffer{}, conform.Options{})
	require.NoError(t, err)
	require.Equal(t, first, readFile(t, filepath.Join(dir, "conformist-impure.nix")),
		"a re-run must not rewrite or drop the persisted allowlist")

	_, err = conform.Run(dir, &bytes.Buffer{}, conform.Options{AllowHosts: []string{"other.example.org"}})
	require.NoError(t, err)
	require.Contains(t, readFile(t, filepath.Join(dir, "conformist-impure.nix")),
		`allowed-hosts = [ "forge.example.org" "other.example.org" ];`)
}

// TestRunLeavesHandWrittenAllowedHostsAlone verifies conform will not edit an
// overlay that sets allowed-hosts itself, and says what to do instead.
func TestRunLeavesHandWrittenAllowedHostsAlone(t *testing.T) {
	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@forge.example.org:sasha/prophit.git")

	const mine = "{ ... }:\n{\n  linters.git-remotes.allowed-hosts = [ \"mine.example\" ];\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "conformist-impure.nix"), []byte(mine), 0o644))

	var out bytes.Buffer
	_, err := conform.Run(dir, &out, conform.Options{})
	require.NoError(t, err)

	require.Equal(t, mine, readFile(t, filepath.Join(dir, "conformist-impure.nix")))
	require.Contains(t, out.String(), "already sets allowed-hosts by hand")
	require.Contains(t, out.String(), managedHostsLine)
}

// TestRunInsertsBeforeClosingBraceOfLetModule verifies placement in the shape the
// Rust templates ship (let … in { … }).
func TestRunInsertsBeforeClosingBraceOfLetModule(t *testing.T) {
	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@forge.example.org:sasha/prophit.git")

	const overlay = "{ pkgs, lib, ... }:\nlet\n  x = 1;\nin\n{\n  settings.linter.clippy = {\n    passes-files = false;\n  };\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "conformist-impure.nix"), []byte(overlay), 0o644))

	_, err := conform.Run(dir, &bytes.Buffer{}, conform.Options{})
	require.NoError(t, err)

	got := readFile(t, filepath.Join(dir, "conformist-impure.nix"))
	require.True(t, strings.HasSuffix(got, "  "+managedHostsLine+"\n}\n"), "managed line is the last attribute, got:\n%s", got)
	require.Contains(t, got, "passes-files = false;", "existing content is preserved")
}

// TestRunAdvisesWhenFlakeDoesNotImportOverlay verifies brownfield flakes whose
// impureEval lacks the import get told, since the allowlist would be inert.
func TestRunAdvisesWhenFlakeDoesNotImportOverlay(t *testing.T) {
	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@forge.example.org:sasha/prophit.git")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("# my own flake.nix\n"), 0o644))

	var out bytes.Buffer
	_, err := conform.Run(dir, &out, conform.Options{})
	require.NoError(t, err)

	require.Contains(t, out.String(), "flake.nix does not import ./conformist-impure.nix")
}

// TestBootstrapPersistsHostsAndStagesOverlay verifies bootstrap applies the
// allowlist to the overlay the template wrote and stages it with the rest.
func TestBootstrapPersistsHostsAndStagesOverlay(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@forge.example.org:sasha/prophit.git")

	var staged []string
	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver: r,
		NoLock:   true,
		FlakeInit: func(_ context.Context, d, _ string) error {
			const overlay = "{ ... }:\n{\n}\n"

			return os.WriteFile(filepath.Join(d, "conformist-impure.nix"), []byte(overlay), 0o644)
		},
		GitStage: func(_ context.Context, _ string, paths []string) error {
			staged = append(staged, paths...)

			return nil
		},
	})
	require.NoError(t, err)

	require.Contains(t, readFile(t, filepath.Join(dir, "conformist-impure.nix")), managedHostsLine)
	require.Equal(t, []string{"conformist-impure.nix"}, staged)
}
