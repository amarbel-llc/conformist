package walk_test

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"
	"time"

	"code.linenisgreat.com/conformist/stats"
	"code.linenisgreat.com/conformist/test"
	"code.linenisgreat.com/conformist/walk"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

func TestGitReader(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	// Neutralize the developer/CI git config so the commits this test makes do
	// not inherit a forced commit.gpgsign — otherwise `git commit` exits 128
	// when the signing agent is locked. Identity comes from the explicit env
	// below (and the per-repo `git config` calls); mirrors cmd/commit_test.go.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "conformist-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conformist-test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "conformist-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conformist-test@example.invalid")

	tempDir := test.TempExamples(t)

	// init a git repo
	cmd := exec.CommandContext(t.Context(), "git", "init")
	cmd.Dir = tempDir
	as.NoError(cmd.Run(), "failed to init git repository")

	// configure git username and email locally
	cmd = exec.CommandContext(t.Context(), "git", "config", "user.name", "testing")
	cmd.Dir = tempDir
	as.NoError(cmd.Run(), "failed to set git username")

	cmd = exec.CommandContext(t.Context(), "git", "config", "user.email", "testing@example.com")
	cmd.Dir = tempDir
	as.NoError(cmd.Run(), "failed to set git email")

	// read empty worktree
	statz := stats.New()
	reader, err := walk.NewGitReader(tempDir, "", &statz)
	as.NoError(err)

	files := make([]*walk.File, 34)
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	n, err := reader.Read(ctx, files)

	cancel()
	as.Equal(33, n)
	as.ErrorIs(err, io.EOF)

	// add a git submodule
	tempSubmoduleDir := test.TempExamples(t)
	cmd = exec.CommandContext(t.Context(), "git", "init")
	cmd.Dir = tempSubmoduleDir
	as.NoError(cmd.Run(), "failed to init git submodule repository")

	// configure git username and email locally for the submodule
	cmd = exec.CommandContext(t.Context(), "git", "config", "user.name", "testing")
	cmd.Dir = tempSubmoduleDir
	as.NoError(cmd.Run(), "failed to set submodule git username")

	cmd = exec.CommandContext(t.Context(), "git", "config", "user.email", "testing@example.com")
	cmd.Dir = tempSubmoduleDir
	as.NoError(cmd.Run(), "failed to set submodule git email")

	// add everything to the submodule's git index
	cmd = exec.CommandContext(t.Context(), "git", "add", ".")
	cmd.Dir = tempSubmoduleDir
	as.NoError(cmd.Run(), "failed to add everything to the submodule index")

	// commit the submodule
	cmd = exec.CommandContext(t.Context(), "git", "commit", "-m", "submodule")
	cmd.Dir = tempSubmoduleDir
	as.NoError(cmd.Run(), "failed to commit the submodule")

	// add the submodule to the main git repository
	// https://github.blog/open-source/git/git-security-vulnerabilities-announced/#cve-2022-39253
	// use -c to pass protocol.file.allow since submodule clone spawns a subprocess that won't see local config
	cmd = exec.CommandContext(t.Context(), "git", "-c", "protocol.file.allow=always", "submodule", "add", tempSubmoduleDir)
	cmd.Dir = tempDir
	as.NoError(cmd.Run(), "failed to add the submodule to the main repository")

	// add everything to the git index
	cmd = exec.CommandContext(t.Context(), "git", "add", ".")
	cmd.Dir = tempDir
	as.NoError(cmd.Run(), "failed to add everything to the index")

	statz = stats.New()
	reader, err = walk.NewGitReader(tempDir, "", &statz)
	as.NoError(err)

	count := 0

	for {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)

		files := make([]*walk.File, 8)
		n, err := reader.Read(ctx, files)

		count += n

		cancel()

		if errors.Is(err, io.EOF) {
			break
		}
	}

	as.Equal(34, count)
	as.Equal(34, statz.Value(stats.Traversed))
	as.Equal(0, statz.Value(stats.Matched))
	as.Equal(0, statz.Value(stats.Formatted))
	as.Equal(0, statz.Value(stats.Changed))
}
