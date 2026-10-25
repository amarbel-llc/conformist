package cmd_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/test"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// TestCheckRunsNoRepairOrStage pins RFC 0003 §4's matrix row for `check`
// (conformist#73): over a tree with a repair-capable, restage-opted-in linter,
// `conformist check` mutates nothing — no file write and no index change — even
// though the committed facade is stale and the repair stub would regenerate it.
func TestCheckRunsNoRepairOrStage(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := t.TempDir()
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

	aux := t.TempDir()
	srcPath := filepath.Join("internal", "foo.src")
	genPath := filepath.Join("pkgs", "foo.generated")
	stub := codegenStub(t, aux, "pkgs/foo.generated")

	as.NoError(os.MkdirAll(filepath.Join(tempDir, "internal"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "pkgs"), 0o755))
	as.NoError(os.WriteFile(srcPath, []byte("original\n"), 0o644))
	as.NoError(os.WriteFile(genPath, []byte("// generated from original\n"), 0o644))

	passesFiles := false
	test.WriteConfig(t, filepath.Join(tempDir, "conformist.toml"), &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"codegen": {
				Command:              "true",
				RepairCommand:        stub,
				Includes:             []string{"internal/*"},
				PassesFiles:          &passesFiles,
				RestageRepairOutputs: true,
				StageNewOutputs:      true,
			},
		},
	})

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	// stage an edit so the facade is stale: a repair run WOULD regenerate it.
	as.NoError(os.WriteFile(srcPath, []byte("edited\n"), 0o644))
	git("add", "internal/foo.src")

	statusBefore := git("status", "--porcelain=v1")
	cachedBefore := git("diff", "--cached")

	conformist(t, withArgs("check", "--no-cache"), withNoError(t))

	// no file write: the stale facade is byte-identical.
	facade, err := os.ReadFile(genPath)
	as.NoError(err)
	as.Equal("// generated from original\n", string(facade))

	// no index change, no new or modified paths anywhere in the tree.
	as.Equal(statusBefore, git("status", "--porcelain=v1"))
	as.Equal(cachedBefore, git("diff", "--cached"))
}
