package cmd_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/test"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// codegenStub writes an executable whole-tree repair stub that regenerates a
// sibling file (dst) from a source file (src), both tree-root-relative. It
// stands in for a codegen-repair lane (dagnabit export, tommy gen, …) whose
// repair-command rewrites a generated file OTHER than the staged source
// (conformist#55). The stub writes "// generated from <src contents>" so the
// regenerated content is observable.
func codegenStub(t *test_ui.T, dir, src, dst string) string {
	t.Helper()

	script := filepath.Join(dir, "codegen-"+strings.ReplaceAll(dst, "/", "_")+".sh")
	body := "#!/usr/bin/env bash\n" +
		"set -euo pipefail\n" +
		"mkdir -p \"$(dirname '" + dst + "')\"\n" +
		"printf '// generated from %s\\n' \"$(cat '" + src + "')\" > '" + dst + "'\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	return script
}

// TestStagedRestagesRepairOutputs pins conformist#55: a whole-tree codegen-repair
// linter that opts into restage-repair-outputs has the files its repair-command
// regenerates restaged by --staged, even though they were never in the index —
// so the commit carries the regenerated output instead of stranding it
// modified-but-unstaged.
func TestStagedRestagesRepairOutputs(tt *testing.T) {
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

	// source under internal/, generated facade under pkgs/. The stub lives
	// outside the tree so it is not itself a matched/walked file.
	aux := t.TempDir()
	srcPath := filepath.Join("internal", "foo.src")
	genPath := filepath.Join("pkgs", "foo.generated")
	stub := codegenStub(t, aux, "internal/foo.src", "pkgs/foo.generated")

	as.NoError(os.MkdirAll(filepath.Join(tempDir, "internal"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "pkgs"), 0o755))
	as.NoError(os.WriteFile(srcPath, []byte("original\n"), 0o644))
	// committed-stale facade derived from the original source
	as.NoError(os.WriteFile(genPath, []byte("// generated from original\n"), 0o644))

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"codegen": {
				Command:              "true", // read-only check is a no-op for this test
				RepairCommand:        stub,
				Includes:             []string{"internal/*"},
				PassesFiles:          &passesFiles,
				RestageRepairOutputs: true,
			},
		},
	}
	test.WriteConfig(t, configPath, cfg)

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	head := git("rev-parse", "HEAD")

	// edit the source so the committed facade is now stale, and stage ONLY the
	// source — mirroring an author who forgot to regenerate.
	as.NoError(os.WriteFile(srcPath, []byte("edited\n"), 0o644))
	git("add", "internal/foo.src")

	// --exit-zero-on-fix: a successful restage is the success path for a hook.
	conformist(
		t,
		withArgs("--staged", "--exit-zero-on-fix", "--no-cache"),
		withNoError(t),
	)

	// the repair regenerated the facade on disk from the edited source ...
	regenerated, err := os.ReadFile(genPath)
	as.NoError(err)
	as.Equal("// generated from edited\n", string(regenerated))

	// ... and #55's restage put BOTH the staged source and the regenerated
	// facade into the index, even though the facade was never staged by the author
	cached := strings.Fields(git("diff", "--cached", "--name-only"))
	as.ElementsMatch([]string{"internal/foo.src", "pkgs/foo.generated"}, cached)

	// the regenerated facade has no leftover unstaged delta (it was restaged,
	// not stranded modified-but-unstaged — the bug #55 fixes)
	as.Empty(git("diff", "--name-only", "--", "pkgs/foo.generated"))

	// no commit was created — the commit is the caller's
	as.Equal(head, git("rev-parse", "HEAD"))
}

// TestStagedRepairOutputsOptInGate pins the opt-in gate of conformist#55: WITHOUT
// restage-repair-outputs, a codegen-repair linter's output is regenerated on
// disk but left modified-but-UNSTAGED (today's safe default). It also pins
// per-linter attribution: an opt-in linter running alongside a non-opt-in one
// restages only its OWN output.
func TestStagedRepairOutputsOptInGate(tt *testing.T) {
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
	// optIn linter regenerates pkgs/opted.generated; plain linter regenerates
	// pkgs/plain.generated. Both trigger on the same staged source edit.
	optInGen := filepath.Join("pkgs", "opted.generated")
	plainGen := filepath.Join("pkgs", "plain.generated")
	optInStub := codegenStub(t, aux, "internal/foo.src", "pkgs/opted.generated")
	plainStub := codegenStub(t, aux, "internal/foo.src", "pkgs/plain.generated")

	as.NoError(os.MkdirAll(filepath.Join(tempDir, "internal"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "pkgs"), 0o755))
	as.NoError(os.WriteFile(srcPath, []byte("original\n"), 0o644))
	as.NoError(os.WriteFile(optInGen, []byte("// generated from original\n"), 0o644))
	as.NoError(os.WriteFile(plainGen, []byte("// generated from original\n"), 0o644))

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"opted": {
				Command:              "true",
				RepairCommand:        optInStub,
				Includes:             []string{"internal/*"},
				PassesFiles:          &passesFiles,
				RestageRepairOutputs: true,
			},
			"plain": {
				Command:       "true",
				RepairCommand: plainStub,
				Includes:      []string{"internal/*"},
				PassesFiles:   &passesFiles,
				// RestageRepairOutputs defaults false: outputs stay unstaged.
			},
		},
	}
	test.WriteConfig(t, configPath, cfg)

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	as.NoError(os.WriteFile(srcPath, []byte("edited\n"), 0o644))
	git("add", "internal/foo.src")

	conformist(
		t,
		withArgs("--staged", "--exit-zero-on-fix", "--no-cache"),
		withNoError(t),
	)

	// both facades were regenerated on disk (both repairs ran) ...
	optedContent, err := os.ReadFile(optInGen)
	as.NoError(err)
	as.Equal("// generated from edited\n", string(optedContent))

	plainContent, err := os.ReadFile(plainGen)
	as.NoError(err)
	as.Equal("// generated from edited\n", string(plainContent))

	// ... but only the opt-in linter's output was restaged alongside the source.
	cached := strings.Fields(git("diff", "--cached", "--name-only"))
	as.ElementsMatch([]string{"internal/foo.src", "pkgs/opted.generated"}, cached)

	// the plain (non-opt-in) linter's output is left modified-but-unstaged —
	// today's safe default, and proof the opt-in is per-linter.
	as.Equal("pkgs/plain.generated", git("diff", "--name-only"))
}
