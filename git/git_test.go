package git_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"code.linenisgreat.com/conformist/git"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

func TestOriginRepoName(tt *testing.T) {
	t := &test_ui.T{T: tt}

	t.Run(test_ui.MakeTestCaseInfo("parses remote URL forms"), func(t *test_ui.T) {
		dir := t.TempDir()
		gitRun(t, dir, "init")
		gitRun(t, dir, "remote", "add", "origin", "placeholder")

		cases := map[string]string{
			"git@github.com:amarbel-llc/conformist.git":       "conformist",
			"git@github.com:amarbel-llc/conformist":           "conformist",
			"https://github.com/amarbel-llc/conformist.git":   "conformist",
			"https://github.com/amarbel-llc/conformist/":      "conformist",
			"ssh://git@github.com/amarbel-llc/go-lib-mcp.git": "go-lib-mcp",
			"/srv/git/conformist.git":                         "conformist",
		}
		for url, want := range cases {
			gitRun(t, dir, "remote", "set-url", "origin", url)
			got, err := git.OriginRepoName(dir)
			require.NoError(t, err)
			require.Equalf(t, want, got, "url %q", url)
		}
	})

	t.Run(test_ui.MakeTestCaseInfo("repo without origin remote"), func(t *test_ui.T) {
		dir := t.TempDir()
		gitRun(t, dir, "init")

		got, err := git.OriginRepoName(dir)
		require.NoError(t, err)
		require.Empty(t, got, "no origin remote must yield empty, not an error")
	})

	t.Run(test_ui.MakeTestCaseInfo("non-git directory"), func(t *test_ui.T) {
		dir := t.TempDir()
		// $TMPDIR can live inside a git worktree (the conformist repo
		// itself), so fence git's upward repo search at the temp dir's
		// parent — otherwise `git config` would resolve the enclosing
		// repo's origin.
		t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

		got, err := git.OriginRepoName(dir)
		require.NoError(t, err)
		require.Empty(t, got, "outside a git repo must yield empty, not an error")
	})
}

// TestHostFromRemoteURL pins the host extraction to the git-remotes linter's
// origin_host (nix/linters/git-remotes.nix): scp-like, ssh:// and other
// scheme:// forms yield a host; local paths and hostless file:// URLs yield "".
func TestHostFromRemoteURL(tt *testing.T) {
	t := &test_ui.T{T: tt}

	t.Run(test_ui.MakeTestCaseInfo("host extraction matches the linter"), func(t *test_ui.T) {
		cases := map[string]string{
			"git@code.linenisgreat.com:conformist.git":              "code.linenisgreat.com",
			"ssh://git@forge.starbrandshoes.com/sasha/prophit.git":  "forge.starbrandshoes.com",
			"ssh://git@forge.starbrandshoes.com:2222/sasha/prophit": "forge.starbrandshoes.com",
			"https://github.com/amarbel-llc/conformist.git":         "github.com",
			"https://user@example.org/some/where.git":               "example.org",
			"forge.example:owner/repo.git":                          "",
			"/srv/git/conformist.git":                               "",
			"file:///srv/git/conformist.git":                        "",
			"../relative/path.git":                                  "",
		}
		for url, want := range cases {
			require.Equalf(t, want, git.HostFromRemoteURL(url), "url %q", url)
		}
	})
}

func gitRun(t *test_ui.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}
