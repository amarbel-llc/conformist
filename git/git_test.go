package git_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/amarbel-llc/conformist/git"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/test_ui"
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

func gitRun(t *test_ui.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}
