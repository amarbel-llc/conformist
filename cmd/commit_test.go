package cmd_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/conformist/cmd"
	formatCmd "github.com/amarbel-llc/conformist/cmd/format"
	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/test"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// TestCommit covers the --commit flow (#24): after formatting, conformist
// stages exactly the files the run changed and creates a
// `chore: conformist fmt+fix` commit. Exit codes: 0 = tree was already
// conformant, 3 = fixes were applied and committed, 2 = refused (dirty tree,
// not a git worktree) or operational error.
func TestCommit(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	// Neutralize the developer/CI git config (commit signing, templates,
	// hooks) for both this test's git calls and the commit conformist
	// creates; identity comes from the explicit env below.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "conformist-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conformist-test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "conformist-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conformist-test@example.invalid")

	git := func(args ...string) string {
		t.Helper()

		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		as.NoError(err, "git %v: %s", args, out)

		return strings.TrimSpace(string(out))
	}

	// test-fmt-append always mutates its target, standing in for a formatter
	// that reflows a mis-formatted file.
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"append": {
				Command:  "test-fmt-append",
				Options:  []string{"hello"},
				Includes: []string{"ruby/*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	// --commit outside a git worktree is refused (exit 2)
	conformist(t,
		withArgs("--commit"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrCommitRefused)
			as.Equal(2, cmd.ExitCode(err))
		}),
	)

	// the refusal must happen before any formatting
	ruby, err := os.ReadFile("ruby/bundler.rb")
	as.NoError(err)
	as.NotContains(string(ruby), "hello")

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	headBefore := git("rev-parse", "HEAD")

	// a dirty tracked file refuses --commit (exit 2): no formatting, no commit
	mainGo := filepath.Join("go", "main.go")
	as.NoError(os.WriteFile(mainGo, []byte("package main\n"), 0o644))

	conformist(t,
		withArgs("--commit"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrCommitRefused)
			as.Equal(2, cmd.ExitCode(err))
		}),
	)
	as.Equal(headBefore, git("rev-parse", "HEAD"))

	git("checkout", "--", mainGo)

	// clean tree: the reformatted file is committed, exit 3
	conformist(t,
		withArgs("--commit"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrFixesCommitted)
			as.Equal(3, cmd.ExitCode(err))
		}),
	)

	headFixed := git("rev-parse", "HEAD")
	as.NotEqual(headBefore, headFixed, "a fix commit should have been created")
	as.Equal("chore: conformist fmt+fix", git("log", "-1", "--format=%s"))
	as.Equal("ruby/bundler.rb", git("show", "--name-only", "--format=", "HEAD"),
		"exactly the reformatted file should be committed")
	as.Empty(git("status", "--porcelain", "--untracked-files=no"),
		"the tracked tree should be clean after the fix commit")

	// second run: nothing left to fix (change detection skips the file),
	// exit 0, no new commit
	conformist(t,
		withArgs("--commit"),
		withNoError(t),
	)
	as.Equal(headFixed, git("rev-parse", "HEAD"))

	// --ci implies fail-on-change, which contradicts committing: refused
	// before any formatting (exit 2)
	conformist(t,
		withArgs("--commit", "--ci"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrCommitRefused)
			as.Equal(2, cmd.ExitCode(err))
		}),
	)
	as.Equal(headFixed, git("rev-parse", "HEAD"))

	// --allow-dirty: an unrelated dirty file is excluded from the commit and
	// left dirty in the working tree (--no-cache forces a reformat of the
	// otherwise-skipped target)
	as.NoError(os.WriteFile(mainGo, []byte("package main\n"), 0o644))

	conformist(t,
		withArgs("--commit", "--allow-dirty", "--no-cache"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrFixesCommitted)
			as.Equal(3, cmd.ExitCode(err))
		}),
	)

	as.Equal("ruby/bundler.rb", git("show", "--name-only", "--format=", "HEAD"),
		"the pre-existing dirty file must not be swept into the fix commit")
	// the helper trims the leading XY-column space from the porcelain output
	as.Equal("M go/main.go", git("status", "--porcelain", "--untracked-files=no"),
		"the pre-existing dirty file should remain dirty in the working tree")
}

// TestCommitAmend covers --commit --amend (#33): instead of a fresh fix
// commit, the run folds its changes into HEAD with `git commit --amend
// --no-edit`, keeping the message. It refuses (exit 2) when there is no HEAD to
// amend or when HEAD is already pushed, and the bare flag requires --commit.
func TestCommitAmend(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "conformist-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conformist-test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "conformist-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conformist-test@example.invalid")

	git := func(args ...string) string {
		t.Helper()

		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		as.NoError(err, "git %v: %s", args, out)

		return strings.TrimSpace(string(out))
	}

	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"append": {
				Command:  "test-fmt-append",
				Options:  []string{"hello"},
				Includes: []string{"ruby/*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	rubyPath := filepath.Join("ruby", "bundler.rb")

	// --amend without --commit is rejected (guarded in root.go, before any
	// worktree state is consulted)
	conformist(t,
		withArgs("--amend"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "--amend requires --commit")
		}),
	)

	git("init")

	// no HEAD yet: --commit --amend is refused (exit 2) before any formatting
	conformist(t,
		withArgs("--commit", "--amend"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrCommitRefused)
			as.ErrorContains(err, "nothing to amend")
			as.Equal(2, cmd.ExitCode(err))
		}),
	)

	emptyRepoRuby, err := os.ReadFile(rubyPath)
	as.NoError(err)
	as.NotContains(string(emptyRepoRuby), "hello", "the empty-repo refusal must happen before formatting")

	git("add", ".")
	git("commit", "-m", "init")

	headBefore := git("rev-parse", "HEAD")

	// clean tree: the reformatted file is folded into HEAD, exit 3
	conformist(t,
		withArgs("--commit", "--amend"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrFixesCommitted)
			as.Equal(3, cmd.ExitCode(err))
		}),
	)

	headAmended := git("rev-parse", "HEAD")
	as.NotEqual(headBefore, headAmended, "HEAD should have been amended to a new sha")
	as.Equal("init", git("log", "-1", "--format=%s"), "--no-edit must keep the existing message")
	as.Contains(git("show", "HEAD:ruby/bundler.rb"), "hello",
		"the reformatted file should be folded into HEAD's tree")
	as.Empty(git("status", "--porcelain", "--untracked-files=no"),
		"the tracked tree should be clean after the amend")

	// once HEAD is pushed, amending is refused (exit 2): rewriting published
	// history is unsafe. Push to a bare remote so origin/<branch> contains HEAD.
	remoteDir := t.TempDir()
	git("init", "--bare", remoteDir)
	git("remote", "add", "origin", remoteDir)
	git("push", "-u", "origin", "HEAD")

	prePush, err := os.ReadFile(rubyPath)
	as.NoError(err)

	// --no-cache forces a reformat so a fix WOULD be produced; the pushed-HEAD
	// refusal must still short-circuit before any formatting
	conformist(t,
		withArgs("--commit", "--amend", "--no-cache"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrCommitRefused)
			as.ErrorContains(err, "already pushed")
			as.Equal(2, cmd.ExitCode(err))
		}),
	)

	as.Equal(headAmended, git("rev-parse", "HEAD"), "a refused amend must not move HEAD")

	postPush, err := os.ReadFile(rubyPath)
	as.NoError(err)
	as.Equal(string(prePush), string(postPush), "the pushed-HEAD refusal must happen before formatting")
}

// TestCommitExitZeroOnFix covers --exit-zero-on-fix (#35): a successful repair
// commit exits 0 instead of 3 (for callers that gate on nonzero=abort, e.g. a
// spinclass pre-merge repair hook), while the commit still happens. The flag
// requires --commit.
func TestCommitExitZeroOnFix(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "conformist-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conformist-test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "conformist-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conformist-test@example.invalid")

	git := func(args ...string) string {
		t.Helper()

		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		as.NoError(err, "git %v: %s", args, out)

		return strings.TrimSpace(string(out))
	}

	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"append": {
				Command:  "test-fmt-append",
				Options:  []string{"hello"},
				Includes: []string{"ruby/*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	// --exit-zero-on-fix without --commit is rejected
	conformist(t,
		withArgs("--exit-zero-on-fix"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "--exit-zero-on-fix requires --commit")
		}),
	)

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	headBefore := git("rev-parse", "HEAD")

	// clean tree: the reformatted file is committed, but the flag downgrades
	// the "fixes applied" exit 3 to 0 (withNoError)
	conformist(t,
		withArgs("--commit", "--exit-zero-on-fix"),
		withNoError(t),
	)

	as.NotEqual(headBefore, git("rev-parse", "HEAD"), "a fix commit should still have been created")
	as.Equal("chore: conformist fmt+fix", git("log", "-1", "--format=%s"))
	as.Empty(git("status", "--porcelain", "--untracked-files=no"),
		"the tracked tree should be clean after the fix commit")
}

// TestCommitTrailer covers --trailer (#26): extra trailers are appended to
// the fix commit's message, and the flag requires --commit.
func TestCommitTrailer(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "conformist-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conformist-test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "conformist-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conformist-test@example.invalid")

	git := func(args ...string) string {
		t.Helper()

		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		as.NoError(err, "git %v: %s", args, out)

		return strings.TrimSpace(string(out))
	}

	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"append": {
				Command:  "test-fmt-append",
				Options:  []string{"hello"},
				Includes: []string{"ruby/*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	// --trailer without --commit is rejected
	conformist(t,
		withArgs("--trailer", "X-Fixed-By: conformist-test"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "--trailer requires --commit")
		}),
	)

	// trailers are appended to the fix commit message
	conformist(t,
		withArgs(
			"--commit",
			"--trailer", "X-Fixed-By: conformist-test",
			"--trailer", "X-Pilot: ssh-agent-mux",
		),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrFixesCommitted)
			as.Equal(3, cmd.ExitCode(err))
		}),
	)

	as.Equal("chore: conformist fmt+fix", git("log", "-1", "--format=%s"))

	trailers := git("log", "-1", "--format=%(trailers)")
	as.Contains(trailers, "X-Fixed-By: conformist-test")
	as.Contains(trailers, "X-Pilot: ssh-agent-mux")
}

// TestStaged covers the lint-staged-style --staged mode (#25): format only
// the files staged in the index, restage the formatted content, create no
// commit. Exit codes: 0 = staged content already conformant, 3 = reformatted
// and restaged, 2 = refused (partially staged files, no git worktree).
func TestStaged(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "conformist-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conformist-test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "conformist-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conformist-test@example.invalid")

	git := func(args ...string) string {
		t.Helper()

		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		as.NoError(err, "git %v: %s", args, out)

		return strings.TrimSpace(string(out))
	}

	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"append": {
				Command:  "test-fmt-append",
				Options:  []string{"hello"},
				Includes: []string{"ruby/*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	// outside a git worktree → refused (exit 2)
	conformist(t,
		withArgs("--staged"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrStagedRefused)
			as.Equal(2, cmd.ExitCode(err))
		}),
	)

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	head := git("rev-parse", "HEAD")

	// nothing staged → nothing to do (exit 0)
	conformist(t,
		withArgs("--staged"),
		withNoError(t),
	)

	// stage a change to a matched file; leave an unrelated file dirty and
	// unstaged — staged mode must tolerate it (that is its whole point)
	rubyPath := filepath.Join("ruby", "bundler.rb")
	as.NoError(os.WriteFile(rubyPath, []byte("puts 'staged change'\n"), 0o644))
	git("add", rubyPath)

	mainGo := filepath.Join("go", "main.go")
	as.NoError(os.WriteFile(mainGo, []byte("package main\n"), 0o644))

	conformist(t,
		withArgs("--staged"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrFixesRestaged)
			as.Equal(3, cmd.ExitCode(err))
		}),
	)

	// the formatted content was restaged: the index blob carries the fix and
	// the staged file has no unstaged delta left
	as.Contains(git("show", ":ruby/bundler.rb"), "hello")
	as.Empty(git("diff", "--name-only", "--", "ruby/bundler.rb"))
	// still staged and NOT committed — the commit is the caller's
	as.Equal("ruby/bundler.rb", git("diff", "--cached", "--name-only"))
	as.Equal(head, git("rev-parse", "HEAD"))
	// the unrelated dirty file is untouched and still unstaged
	as.Equal("go/main.go", git("diff", "--name-only"))

	// second run: staged content is now conformant → exit 0, index unchanged
	stagedBlob := git("show", ":ruby/bundler.rb")

	conformist(t,
		withArgs("--staged"),
		withNoError(t),
	)
	as.Equal(stagedBlob, git("show", ":ruby/bundler.rb"))

	// a staged file with ADDITIONAL unstaged edits is refused before any
	// formatting (the restage would sweep the unstaged hunk into the index)
	preRefusal, err := os.ReadFile(rubyPath)
	as.NoError(err)
	as.NoError(os.WriteFile(rubyPath, append(preRefusal, []byte("puts 'unstaged extra'\n")...), 0o644))

	conformist(t,
		withArgs("--staged", "--no-cache"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, formatCmd.ErrStagedRefused)
			as.ErrorContains(err, "partially staged")
			as.ErrorContains(err, "ruby/bundler.rb")
			as.Equal(2, cmd.ExitCode(err))
		}),
	)

	// refusal happened before formatting: the worktree gained no new append
	postRefusal, err := os.ReadFile(rubyPath)
	as.NoError(err)
	as.Equal(string(preRefusal)+"puts 'unstaged extra'\n", string(postRefusal))

	// flag interactions
	conformist(t,
		withArgs("--staged", "--commit"),
		withError(func(as *require.Assertions, err error) {
			as.Error(err)
		}),
	)
	conformist(t,
		withArgs("--staged", "--trailer", "X-Fixed-By: conformist-test"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "--trailer requires --commit")
		}),
	)
	conformist(t,
		withArgs("--staged", "ruby"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "positional paths")
		}),
	)
}

// TestCommitStdin asserts --commit refuses stdin mode: there is no working
// tree state to commit when formatting a stream.
func TestCommitStdin(tt *testing.T) {
	t := &test_ui.T{T: tt}
	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:  "echo",
				Includes: []string{"*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	conformist(t,
		withArgs("--commit", "--stdin", "ruby/bundler.rb"),
		withError(func(as *require.Assertions, err error) {
			as.Error(err)
			as.Equal(2, cmd.ExitCode(err))
		}),
	)
}
