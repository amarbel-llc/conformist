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

// codegenSrc is the tree-root-relative source file every codegenStub
// regenerates from; the staged-repair tests edit and stage exactly this path.
const codegenSrc = "internal/foo.src"

// codegenStub writes an executable whole-tree repair stub that regenerates a
// sibling file (dst, tree-root-relative) from codegenSrc. It stands in for a
// codegen-repair lane (dagnabit export, tommy gen, …) whose repair-command
// rewrites a generated file OTHER than the staged source (conformist#55). The
// stub writes "// generated from <src contents>" so the regenerated content is
// observable.
func codegenStub(t *test_ui.T, dir, dst string) string {
	t.Helper()

	script := filepath.Join(dir, "codegen-"+strings.ReplaceAll(dst, "/", "_")+".sh")
	body := "#!/usr/bin/env bash\n" +
		"set -euo pipefail\n" +
		"mkdir -p \"$(dirname '" + dst + "')\"\n" +
		"printf '// generated from %s\\n' \"$(cat '" + codegenSrc + "')\" > '" + dst + "'\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	return script
}

// deleteStub writes an executable whole-tree repair stub that deletes a
// tree-root-relative victim path. It stands in for a package-move codegen
// (dewey-reposition) whose repair-command removes a relocated file
// (conformist#57). The stub is idempotent (rm -f), so re-runs are harmless.
func deleteStub(t *test_ui.T, dir, victim string) string {
	t.Helper()

	script := filepath.Join(dir, "delete-"+strings.ReplaceAll(victim, "/", "_")+".sh")
	body := "#!/usr/bin/env bash\n" +
		"set -euo pipefail\n" +
		"rm -f '" + victim + "'\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	return script
}

// unformattedCodegenStub is codegenStub's deliberately-misformatted twin: it
// regenerates dst with three trailing spaces on the generated line, standing in
// for a whole-tree repair-command (doppelgang --fix) whose output is NOT itself
// formatter-normalized (conformist#70). A matching formatter must strip that
// trailing whitespace before the --staged commit lands, or the index carries the
// raw repair output.
func unformattedCodegenStub(t *test_ui.T, dir, dst string) string {
	t.Helper()

	script := filepath.Join(dir, "codegen-unformatted-"+strings.ReplaceAll(dst, "/", "_")+".sh")
	body := "#!/usr/bin/env bash\n" +
		"set -euo pipefail\n" +
		"mkdir -p \"$(dirname '" + dst + "')\"\n" +
		"printf '// generated from %s   \\n' \"$(cat '" + codegenSrc + "')\" > '" + dst + "'\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	return script
}

// stripTrailingWSFormatterStub writes an executable formatter stub that strips
// trailing whitespace from each file passed as a positional argument, rewriting
// it in place. It stands in for nixfmt normalizing flake.nix after doppelgang's
// byte-splice edit (conformist#70). A temp-file swap is used instead of `sed -i`
// to avoid GNU/BSD in-place-edit divergence.
func stripTrailingWSFormatterStub(t *test_ui.T, dir string) string {
	t.Helper()

	script := filepath.Join(dir, "strip-trailing-ws.sh")
	body := "#!/usr/bin/env bash\n" +
		"set -euo pipefail\n" +
		"for f in \"$@\"; do\n" +
		"  tmp=\"$(mktemp)\"\n" +
		"  sed 's/[[:space:]]*$//' \"$f\" > \"$tmp\"\n" +
		"  mv \"$tmp\" \"$f\"\n" +
		"done\n"
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
	stub := codegenStub(t, aux, "pkgs/foo.generated")

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
	optInStub := codegenStub(t, aux, "pkgs/opted.generated")
	plainStub := codegenStub(t, aux, "pkgs/plain.generated")

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

// TestStagedStagesNewRepairOutputs pins conformist#56 (tier 3): a whole-tree
// codegen-repair linter that opts into BOTH restage-repair-outputs and
// stage-new-outputs has the brand-new (untracked) files its repair-command
// creates staged by --staged, in addition to the tier-2 modified outputs.
func TestStagedStagesNewRepairOutputs(tt *testing.T) {
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
	// the generated facade does NOT exist at commit time, so the repair CREATES
	// it as a brand-new untracked file.
	genPath := filepath.Join("pkgs", "foo.generated")
	stub := codegenStub(t, aux, "pkgs/foo.generated")

	as.NoError(os.MkdirAll(filepath.Join(tempDir, "internal"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "pkgs"), 0o755))
	as.NoError(os.WriteFile(srcPath, []byte("original\n"), 0o644))

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
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
	}
	test.WriteConfig(t, configPath, cfg)

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	head := git("rev-parse", "HEAD")

	// edit + stage ONLY the source; the facade does not exist yet.
	as.NoError(os.WriteFile(srcPath, []byte("edited\n"), 0o644))
	git("add", "internal/foo.src")

	conformist(
		t,
		withArgs("--staged", "--exit-zero-on-fix", "--no-cache"),
		withNoError(t),
	)

	// the repair created the facade on disk ...
	regenerated, err := os.ReadFile(genPath)
	as.NoError(err)
	as.Equal("// generated from edited\n", string(regenerated))

	// ... and tier 3 staged the brand-new file alongside the source.
	cached := strings.Fields(git("diff", "--cached", "--name-only"))
	as.ElementsMatch([]string{"internal/foo.src", "pkgs/foo.generated"}, cached)

	// the new file is fully staged as an addition (A ) with no unstaged delta —
	// not left untracked (??) and not modified-but-unstaged.
	as.Equal("A  pkgs/foo.generated", git("status", "--porcelain", "--", "pkgs/foo.generated"))
	as.Empty(git("diff", "--name-only", "--", "pkgs/foo.generated"))

	as.Equal(head, git("rev-parse", "HEAD"))
}

// TestStagedNewOutputsGate pins the tier-3 gate of conformist#56: WITHOUT
// stage-new-outputs (tier 2 alone), a repair-created brand-new file is written
// to disk but left UNTRACKED — staging untracked files is the more dangerous
// capability that tier 3 gates, and tier 2 alone MUST NOT do it (RFC-0002 §2.3).
func TestStagedNewOutputsGate(tt *testing.T) {
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

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"codegen": {
				Command:              "true",
				RepairCommand:        stub,
				Includes:             []string{"internal/*"},
				PassesFiles:          &passesFiles,
				RestageRepairOutputs: true,
				// StageNewOutputs defaults false: tier 2 alone.
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

	// the repair created the facade on disk ...
	regenerated, err := os.ReadFile(genPath)
	as.NoError(err)
	as.Equal("// generated from edited\n", string(regenerated))

	// ... but tier 2 alone did NOT stage it: only the source is in the index.
	cached := strings.Fields(git("diff", "--cached", "--name-only"))
	as.ElementsMatch([]string{"internal/foo.src"}, cached)

	// the new facade is left untracked (??), not staged — the tier-3 gate.
	as.Equal("?? pkgs/foo.generated", git("status", "--porcelain", "--", "pkgs/foo.generated"))
}

// TestStagedStagesDeletedRepairOutputs pins conformist#57 (tier 4): a whole-tree
// codegen-repair linter that opts into BOTH restage-repair-outputs and
// stage-deleted-outputs has the deletions its repair-command performs staged by
// --staged, so a repair-driven removal lands in the commit.
func TestStagedStagesDeletedRepairOutputs(tt *testing.T) {
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
	// victim is a committed tracked file the repair deletes (a relocated file).
	victimPath := filepath.Join("pkgs", "old.generated")
	stub := deleteStub(t, aux, "pkgs/old.generated")

	as.NoError(os.MkdirAll(filepath.Join(tempDir, "internal"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "pkgs"), 0o755))
	as.NoError(os.WriteFile(srcPath, []byte("original\n"), 0o644))
	as.NoError(os.WriteFile(victimPath, []byte("relocated away\n"), 0o644))

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"reposition": {
				Command:              "true",
				RepairCommand:        stub,
				Includes:             []string{"internal/*"},
				PassesFiles:          &passesFiles,
				RestageRepairOutputs: true,
				StageDeletedOutputs:  true,
			},
		},
	}
	test.WriteConfig(t, configPath, cfg)

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	head := git("rev-parse", "HEAD")

	// edit + stage ONLY the source; the repair will delete the victim.
	as.NoError(os.WriteFile(srcPath, []byte("edited\n"), 0o644))
	git("add", "internal/foo.src")

	conformist(
		t,
		withArgs("--staged", "--exit-zero-on-fix", "--no-cache"),
		withNoError(t),
	)

	// the repair removed the victim from disk ...
	_, statErr := os.Stat(victimPath)
	as.True(os.IsNotExist(statErr), "the victim file must be deleted on disk")

	// ... and tier 4 staged the deletion alongside the source.
	cached := strings.Fields(git("diff", "--cached", "--name-only"))
	as.ElementsMatch([]string{"internal/foo.src", "pkgs/old.generated"}, cached)

	// the deletion is staged (D ) with no leftover unstaged removal.
	as.Equal("D  pkgs/old.generated", git("status", "--porcelain", "--", "pkgs/old.generated"))

	as.Equal(head, git("rev-parse", "HEAD"))
}

// TestStagedDeletedOutputsGate pins the tier-4 gate of conformist#57: WITHOUT
// stage-deleted-outputs (tier 2 alone), a repair-driven deletion is performed on
// disk but left UNSTAGED — staging a deletion removes a path from the commit's
// tree, the most destructive mutation, which tier 2 MUST NOT do (RFC-0002 §2.2).
func TestStagedDeletedOutputsGate(tt *testing.T) {
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
	victimPath := filepath.Join("pkgs", "old.generated")
	stub := deleteStub(t, aux, "pkgs/old.generated")

	as.NoError(os.MkdirAll(filepath.Join(tempDir, "internal"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "pkgs"), 0o755))
	as.NoError(os.WriteFile(srcPath, []byte("original\n"), 0o644))
	as.NoError(os.WriteFile(victimPath, []byte("relocated away\n"), 0o644))

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"reposition": {
				Command:              "true",
				RepairCommand:        stub,
				Includes:             []string{"internal/*"},
				PassesFiles:          &passesFiles,
				RestageRepairOutputs: true,
				// StageDeletedOutputs defaults false: tier 2 alone.
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

	// the repair removed the victim from disk ...
	_, statErr := os.Stat(victimPath)
	as.True(os.IsNotExist(statErr), "the repair still runs; only staging the deletion is gated")

	// ... but tier 2 alone did NOT stage the deletion: only the source is staged.
	cached := strings.Fields(git("diff", "--cached", "--name-only"))
	as.ElementsMatch([]string{"internal/foo.src"}, cached)

	// the deletion is left as an unstaged worktree removal (still tracked in the
	// index), not staged — the tier-4 gate.
	as.Equal("pkgs/old.generated", git("ls-files", "--deleted"))
}

// TestStagedFormatsRepairOutputsBeforeRestaging pins conformist#70: a whole-tree
// repair-command that writes an UNFORMATTED file ALSO covered by a formatter,
// run under --staged, lands the FORMATTED content in the index. Repairs run
// before formatters so the formatter pass can normalise autofix output, but the
// formatter pass is scoped to the input paths — in --staged that is the staged
// set. The repair output (here pkgs/foo.generated) sits OUTSIDE the staged set
// (only internal/foo.src was staged), so unless the opt-in linter's repair
// outputs are folded into the formatter scope, the formatter never visits the
// generated file and the raw, unformatted repair output is what gets restaged.
// Mirrors doppelgang --fix rewriting flake.nix (byte-splice, not nixfmt'd) while
// only some other file was staged.
func TestStagedFormatsRepairOutputsBeforeRestaging(tt *testing.T) {
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

	// rawShow returns the staged blob verbatim (no whitespace trimming), so the
	// trailing-whitespace difference between the formatted and unformatted output
	// is observable — the git() helper above trims it away.
	rawShow := func(spec string) string {
		t.Helper()

		out, err := exec.CommandContext(t.Context(), "git", "show", spec).Output()
		as.NoError(err)

		return string(out)
	}

	aux := t.TempDir()
	srcPath := filepath.Join("internal", "foo.src")
	genPath := filepath.Join("pkgs", "foo.generated")
	repair := unformattedCodegenStub(t, aux, "pkgs/foo.generated")
	formatter := stripTrailingWSFormatterStub(t, aux)

	as.NoError(os.MkdirAll(filepath.Join(tempDir, "internal"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "pkgs"), 0o755))
	as.NoError(os.WriteFile(srcPath, []byte("original\n"), 0o644))
	// committed-stale facade derived from the original source (already formatted)
	as.NoError(os.WriteFile(genPath, []byte("// generated from original\n"), 0o644))

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"strip-ws": {
				Command:  formatter,
				Includes: []string{"pkgs/*.generated"},
			},
		},
		LinterConfigs: map[string]*config.Linter{
			"codegen": {
				Command:              "true", // read-only check is a no-op for this test
				RepairCommand:        repair,
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

	// edit + stage ONLY the source; the repair regenerates the facade with
	// trailing whitespace, which the formatter must strip before it is restaged.
	as.NoError(os.WriteFile(srcPath, []byte("edited\n"), 0o644))
	git("add", "internal/foo.src")

	conformist(
		t,
		withArgs("--staged", "--exit-zero-on-fix", "--no-cache"),
		withNoError(t),
	)

	// the on-disk facade was regenerated from the edited source AND
	// formatter-normalised: the repair's trailing whitespace is gone.
	regenerated, err := os.ReadFile(genPath)
	as.NoError(err)
	as.Equal("// generated from edited\n", string(regenerated))

	// the CORE of #70: the STAGED blob is the formatted content, not the raw
	// (trailing-whitespace) repair output. Compared verbatim, since the
	// difference is exactly the trailing whitespace a trim would hide.
	as.Equal("// generated from edited\n", rawShow(":pkgs/foo.generated"))

	// both the source and the regenerated facade are staged (#55 restage scope)
	cached := strings.Fields(git("diff", "--cached", "--name-only"))
	as.ElementsMatch([]string{"internal/foo.src", "pkgs/foo.generated"}, cached)

	// no leftover unstaged delta on the facade — the formatted content is what is
	// both on disk and in the index.
	as.Empty(git("diff", "--name-only", "--", "pkgs/foo.generated"))

	// no commit was created — the commit is the caller's
	as.Equal(head, git("rev-parse", "HEAD"))
}
