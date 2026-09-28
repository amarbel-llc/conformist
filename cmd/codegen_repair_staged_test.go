package cmd_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/cmd"
	"code.linenisgreat.com/conformist/cmd/codegen"
	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/test"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// This file is the end-to-end test for conformist#124: a fixture repo with a stale
// generated file, where the author's own commit triggers the codegen repair and the
// regenerated outputs land in THAT commit's index. It drives the real `--staged`
// path with the real codegen engine — the only stand-in is nix itself, which the Go
// test lane's build sandbox does not have.
//
// Unit coverage for the engine (the convergence loop, the codegenPrefix handling,
// the fail-soft policy) lives in cmd/codegen/codegen_test.go. What is asserted HERE
// is the wiring those tests cannot see: that a codegenPatch's three kinds of output
// — a rewritten tracked file, a brand-new untracked file, and a deleted one — are
// attributed to this linter and staged by tiers #55/#56/#57, and that the engine's
// own intent-to-add bookkeeping does not disturb that attribution.

const (
	codegenHelperEnv     = "CONFORMIST_TEST_CODEGEN_HELPER"
	codegenHelperNixEnv  = "CONFORMIST_TEST_CODEGEN_NIX"
	codegenHelperRootEnv = "CONFORMIST_TEST_CODEGEN_ROOT"

	codegenHelperTest = "TestCodegenRepairHelperProcess"
)

// TestCodegenRepairHelperProcess is not a test of its own: it is how the end-to-end
// test below obtains a real `conformist codegen-repair` EXECUTABLE to hand
// conformist as a repair-command. conformist runs a repair-command as a
// subprocess, and the in-process test harness has no built binary to point at, so
// the test binary re-executes itself here (the standard Go helper-process pattern)
// and runs the genuine engine. It exits explicitly so the test framework's own
// PASS output never reaches conformist as linter output.
func TestCodegenRepairHelperProcess(tt *testing.T) {
	if os.Getenv(codegenHelperEnv) != "1" {
		tt.Skip("helper process: only runs when re-executed by the end-to-end test")
	}

	err := codegen.Run(context.Background(), codegen.Options{
		System:   "x86_64-linux",
		Nix:      os.Getenv(codegenHelperNixEnv),
		TreeRoot: os.Getenv(codegenHelperRootEnv),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	os.Exit(0)
}

// codegenPatches are the patches the stand-in nix serves, one per check. They are
// literal rather than computed because this test needs only one convergence pass:
// each check owns a distinct file, and the recompute-after-apply behaviour is
// covered where it belongs, in the engine's own tests.
//
// Each is shaped exactly like a real codegenPatch — a `git diff --no-index src
// work`, so its paths carry the two synthetic components `git apply -p2` strips —
// and between them they cover all three output kinds a patch can carry.
var codegenPatches = map[string]string{
	// a tracked generated file the author left stale
	"facade": `diff --git a/src/pkgs/foo.generated b/work/pkgs/foo.generated
--- a/src/pkgs/foo.generated
+++ b/work/pkgs/foo.generated
@@ -1 +1 @@
-// generated from original
+// generated from edited
`,
	// a generated file that does not exist yet (conformist#56)
	"extra": `diff --git a/src/pkgs/bar.generated b/work/pkgs/bar.generated
new file mode 100644
--- /dev/null
+++ b/work/pkgs/bar.generated
@@ -0,0 +1 @@
+// generated extra
`,
	// a generated file a package move relocated away (conformist#57)
	"pruned": `diff --git a/src/pkgs/gone.generated b/work/pkgs/gone.generated
deleted file mode 100644
--- a/src/pkgs/gone.generated
+++ /dev/null
@@ -1 +0,0 @@
-// generated once
`,
}

// codegenNixStub writes a stand-in `nix` answering the two invocations the engine
// makes: discovery (`nix eval … --apply`), advertising exactly the named checks, and
// one patch build per check from codegenPatches.
func codegenNixStub(t *test_ui.T, dir string, names ...string) string {
	t.Helper()

	var cases strings.Builder

	for _, name := range names {
		cases.WriteString("  " + name + ")\n")
		cases.WriteString("    cat > \"$out/patch\" <<'STUBPATCH'\n")
		cases.WriteString(codegenPatches[name])
		cases.WriteString("STUBPATCH\n")
		cases.WriteString("    ;;\n")
	}

	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, `{"name":"`+name+`"}`)
	}

	discovery := "[" + strings.Join(entries, ",") + "]"

	script := filepath.Join(dir, "nix")
	body := bashShebang(t) +
		"set -euo pipefail\n" +
		"\n" +
		"if [ \"${1:-}\" = eval ]; then\n" +
		"  cat <<'STUBJSON'\n" +
		discovery + "\n" +
		"STUBJSON\n" +
		"  exit 0\n" +
		"fi\n" +
		"\n" +
		"attr=\"${@: -1}\"\n" +
		"rest=\"${attr#*'#checks.'}\"\n" +
		"name=\"${rest#*.}\"\n" +
		"name=\"${name%%.*}\"\n" +
		"\n" +
		"out=$(mktemp -d)\n" +
		"\n" +
		"case \"$name\" in\n" +
		cases.String() +
		"  *)\n" +
		"    echo \"stub nix: no patch for check $name\" >&2\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n" +
		"\n" +
		"echo \"$out\"\n"

	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	return script
}

// codegenRepairCommand writes the repair-command conformist will run: a wrapper
// re-executing this test binary as the codegen helper process, with the stand-in
// nix and the tree root baked in.
func codegenRepairCommand(t *test_ui.T, dir, nix, treeRoot string) string {
	t.Helper()

	self, err := filepath.Abs(os.Args[0])
	require.NoError(t, err)

	script := filepath.Join(dir, "codegen-repair.sh")
	body := bashShebang(t) +
		"set -euo pipefail\n" +
		"export " + codegenHelperEnv + "=1\n" +
		"export " + codegenHelperNixEnv + "='" + nix + "'\n" +
		"export " + codegenHelperRootEnv + "='" + treeRoot + "'\n" +
		"exec '" + self + "' '-test.run=^" + codegenHelperTest + "$'\n"

	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	return script
}

// TestStagedCodegenRepairRestagesEveryOutputKind is conformist#124's end-to-end
// case. An author edits a source file and commits, having forgotten to regenerate:
// the pre-commit lane's codegen repair builds each check's patch, applies it, and
// the regenerated, created and deleted outputs all land in the index the author's
// own commit is about to use — so the commit is self-consistent and the repo's
// drift check passes on it.
func TestStagedCodegenRepairRestagesEveryOutputKind(tt *testing.T) {
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

	as.NoError(os.MkdirAll(filepath.Join(tempDir, "internal"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "pkgs"), 0o755))

	// The source the author edits, plus the generated siblings as they were last
	// committed: one stale, one soon to be deleted. pkgs/bar.generated does not
	// exist yet — the `extra` check creates it.
	as.NoError(os.WriteFile(filepath.Join("internal", "foo.src"), []byte("original\n"), 0o644))
	as.NoError(os.WriteFile(
		filepath.Join("pkgs", "foo.generated"), []byte("// generated from original\n"), 0o644,
	))
	as.NoError(os.WriteFile(
		filepath.Join("pkgs", "gone.generated"), []byte("// generated once\n"), 0o644,
	))

	// A flake.nix must exist for a real adopter's wrapper to run the repair at
	// all; the stand-in nix never reads it, but keeping it here means the fixture
	// is shaped like a repo this lane would actually fire in.
	as.NoError(os.WriteFile("flake.nix", []byte("{ outputs = _: { }; }\n"), 0o644))

	aux := t.TempDir()
	nix := codegenNixStub(t, aux, "extra", "facade", "pruned")
	repair := codegenRepairCommand(t, aux, nix, tempDir)

	passesFiles := false
	test.WriteConfig(t, filepath.Join(tempDir, "conformist.toml"), &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"codegen-repair": {
				// Repair-only: the read-only command is a no-op, because the
				// repo's own drift checks are the check-mode gate.
				Command:       "true",
				RepairCommand: repair,
				Includes:      []string{"internal/*"},
				PassesFiles:   &passesFiles,
				// the three staging tiers this lane needs
				RestageRepairOutputs: true,
				StageNewOutputs:      true,
				StageDeletedOutputs:  true,
			},
		},
	})

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	head := git("rev-parse", "HEAD")

	// The author edits the source and stages ONLY that, leaving every generated
	// sibling stale — the mistake this lane exists to absorb.
	as.NoError(os.WriteFile(filepath.Join("internal", "foo.src"), []byte("edited\n"), 0o644))
	git("add", "internal/foo.src")

	// --exit-zero-on-fix: for a pre-commit hook, a successful repair is success.
	conformist(
		t,
		withArgs("--staged", "--exit-zero-on-fix", "--no-cache"),
		withNoError(t),
	)

	// Every output kind landed on disk ...
	regenerated, err := os.ReadFile(filepath.Join("pkgs", "foo.generated"))
	as.NoError(err)
	as.Equal("// generated from edited\n", string(regenerated))

	created, err := os.ReadFile(filepath.Join("pkgs", "bar.generated"))
	as.NoError(err)
	as.Equal("// generated extra\n", string(created))

	as.NoFileExists(filepath.Join(tempDir, "pkgs", "gone.generated"))

	// ... and all of it is staged alongside the author's own edit, so the commit
	// they are about to make carries a consistent tree.
	cached := strings.Fields(git("diff", "--cached", "--name-only"))
	as.ElementsMatch(
		[]string{
			"internal/foo.src",
			"pkgs/foo.generated",
			"pkgs/bar.generated",
			"pkgs/gone.generated",
		},
		cached,
	)

	// Nothing was left stranded: no output is modified-but-unstaged (the bug #55
	// fixes) and none is still untracked (the bug #56 fixes).
	as.Empty(git("diff", "--name-only"), "no repair output may be left unstaged")
	as.Empty(git("ls-files", "--others", "--exclude-standard"))

	// The created output is staged with its real CONTENT, not as the empty blob an
	// intent-to-add entry records. The engine adds such an entry so the next
	// convergence pass's nix build can see the new file, then removes it again
	// before returning; if that removal were skipped, the hook would find the path
	// already in the index and this commit would carry an empty file.
	as.Equal(
		"// generated extra",
		git("show", ":pkgs/bar.generated"),
		"the created output must be staged with its content, not as an empty intent-to-add blob",
	)

	// No commit was created: the commit is the caller's.
	as.Equal(head, git("rev-parse", "HEAD"))
}

// TestStagedCodegenRepairBlocksWhenPatchWillNotApply is the other half of the
// contract: when the lane CANNOT do its job, it must stop the commit rather than let
// a stale generated file through quietly.
//
// conformist otherwise discards a repair-command's exit status — most repairs are
// best-effort — so the `repair-must-succeed` opt-in is what carries the failure out.
// The subtest without that flag is the control: it proves the blocking comes from the
// opt-in and not from something incidental about a failing subprocess, which is the
// difference between a real gate and one that is merely believed.
func TestStagedCodegenRepairBlocksWhenPatchWillNotApply(tt *testing.T) {
	t := &test_ui.T{T: tt}

	stagedConflictingCodegen(t, true, withError(func(as *require.Assertions, err error) {
		as.Error(err, "a repair that could not do its job must stop the commit")
		// Exit 2, an operational failure: the repair could not run to completion.
		// Distinct from exit 1 ("a tool reported findings"), so a gate can tell the
		// two apart.
		as.Equal(2, cmd.ExitCode(err), "a blocked repair must exit 2")
		// The repair command's own output travels in the error. That matters
		// because conformist logs repair output below the default level, so the
		// error is the only place the operator learns WHY the commit stopped.
		as.ErrorContains(err, "does not apply to this tree")
	}))
}

// TestStagedCodegenRepairProceedsWithoutMustSucceed is the control for the test
// above. The SAME failing repair, with only `repair-must-succeed` turned off, lets
// the commit proceed — which is what proves the blocking comes from that opt-in
// rather than from something incidental about a subprocess exiting non-zero. Without
// this pairing, the blocking test would pass for a reason nobody had established.
func TestStagedCodegenRepairProceedsWithoutMustSucceed(tt *testing.T) {
	t := &test_ui.T{T: tt}

	stagedConflictingCodegen(t, false, withNoError(t))
}

// stagedConflictingCodegen builds a fixture whose codegen patch cannot apply — the
// committed generated file was hand-edited, so the patch's hunk matches neither
// forwards nor in reverse — stages an unrelated source edit, and runs the real
// `--staged` lane over it. The caller's assertion carries the expectation.
func stagedConflictingCodegen(t *test_ui.T, repairMustSucceed bool, assert option) {
	t.Helper()

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

	as.NoError(os.MkdirAll(filepath.Join(tempDir, "internal"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "pkgs"), 0o755))
	as.NoError(os.WriteFile(filepath.Join("internal", "foo.src"), []byte("original\n"), 0o644))

	// The `facade` patch's hunk expects "// generated from original"; this content
	// is something else, so the patch cannot apply in either direction.
	as.NoError(os.WriteFile(
		filepath.Join("pkgs", "foo.generated"), []byte("hand-edited\n"), 0o644,
	))
	as.NoError(os.WriteFile("flake.nix", []byte("{ outputs = _: { }; }\n"), 0o644))

	aux := t.TempDir()
	nix := codegenNixStub(t, aux, "facade")
	repair := codegenRepairCommand(t, aux, nix, tempDir)

	passesFiles := false
	test.WriteConfig(t, filepath.Join(tempDir, "conformist.toml"), &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"codegen-repair": {
				Command:              "true",
				RepairCommand:        repair,
				Includes:             []string{"internal/*"},
				PassesFiles:          &passesFiles,
				RestageRepairOutputs: true,
				RepairMustSucceed:    repairMustSucceed,
			},
		},
	})

	git("init")
	git("add", ".")
	git("commit", "-m", "init")

	as.NoError(os.WriteFile(filepath.Join("internal", "foo.src"), []byte("edited\n"), 0o644))
	git("add", "internal/foo.src")

	conformist(t, withArgs("--staged", "--exit-zero-on-fix", "--no-cache"), assert)

	// Either way the hand-edited file is untouched — the patch never applied.
	content, err := os.ReadFile(filepath.Join("pkgs", "foo.generated"))
	as.NoError(err)
	as.Equal("hand-edited\n", string(content))
}
