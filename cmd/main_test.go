package cmd_test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain bounds both git tree-root resolution and conformist's own config
// discovery to the temp root for the whole cmd test package (conformist#15).
//
// The integration tests run conformist against fixtures created under $TMPDIR
// (test.TempExamples -> t.TempDir()). When $TMPDIR is itself inside a git
// worktree / monorepo — as in a spinclass session, where $TMPDIR is the
// worktree's .tmp/ — conformist would otherwise escape the (non-git) fixture
// two ways: `git rev-parse --show-toplevel` ascends into the worktree's .git,
// and config.FindUp walks all the way to / and picks up an ancestor
// conformist.toml/treelint.toml (e.g. the monorepo's). Either makes conformist
// treat real tracked files as its tree and run formatters over them. A normal
// $TMPDIR=/tmp has no ancestor repo or config, which is why this is otherwise
// masked.
//
// GIT_CEILING_DIRECTORIES bounds the git subprocess; CONFORMIST_CEILING_DIRECTORIES
// bounds config.FindUp. Setting both to the temp root stops each search there, so
// neither can reach the worktree. Fixtures that need a repo `git init` below the
// ceiling, so they still resolve to their own repo/config. EvalSymlinks to match
// the canonical comparison git and conformist both do.
//
// BLACK_NUM_WORKERS pins the fixture roster's black (test/examples/conformist.toml)
// to a single worker, sidestepping a CPython 3.14 off-by-three that the nixpkgs
// f13ff45 bump dragged in. With more than one .py fixture black takes its
// process-pool path, whose forkserver binds an AF_UNIX socket under $TMPDIR. CPython
// tries to avoid overflowing sun_path (108 on Linux) by falling back to /tmp when
// $TMPDIR is too long, but its budget is wrong: multiprocessing/util.py:179 reserves
// len(tmpdir)+14+14 for "/pymp-XXXXXXXX" + "/sock-XXXXXXXX", while connection.py:83
// actually emits sock-<12 hex> — 18 bytes with its slash, not 14. That leaves a
// four-byte window, len($TMPDIR) in [76, 79], which the fallback waves through and
// bind() then rejects. A spinclass session sits squarely in it: $TMPDIR is the
// worktree's .tmp (62) and `nix develop` appends its own /nix-shell.XXXXXX (17), for
// 79 exactly — so black exits non-zero and every test running the unmodified fixture
// roster fails with ErrFormattingFailures. Note a LONGER $TMPDIR is fine: past 79 the
// fallback engages and the socket lands in /tmp. Only Python 3.14 exposed this at
// all; multiprocessing previously defaulted to fork, which needs no socket. One
// worker skips the pool entirely (black/concurrency.py builds a ProcessPoolExecutor
// only `if workers > 1`); black's own `except OSError` beside that cannot help,
// because the pool spawns lazily and the error escapes later, at submit(). Repointing
// $TMPDIR instead would defeat the conformist#15 guard above, which needs it to stay
// inside the worktree. Reproduce with `just debug-test-go-sunpath-window`.
func TestMain(m *testing.M) {
	if tmp, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		for _, key := range []string{
			"GIT_CEILING_DIRECTORIES",
			"CONFORMIST_CEILING_DIRECTORIES",
		} {
			// Don't clobber an explicit ceiling the environment already set.
			if os.Getenv(key) == "" {
				_ = os.Setenv(key, tmp)
			}
		}
	}

	// Don't clobber an explicit worker count the environment already set.
	if os.Getenv("BLACK_NUM_WORKERS") == "" {
		_ = os.Setenv("BLACK_NUM_WORKERS", "1")
	}

	os.Exit(m.Run())
}
