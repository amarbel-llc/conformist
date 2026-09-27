package codegen

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/git"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// The tests in this file drive the real engine against a STAND-IN nix. They
// cannot use a real one: the Go test lane runs inside a nix build sandbox, which
// has no nix binary and no network (the same constraint that keeps
// explore-merge-driver-flake-lock out of the CI lane). Everything below the nix
// boundary is real — real patches produced by real `git diff --no-index`, applied
// by the real `git apply`, against a real git worktree — so what the stub replaces
// is only the derivation build, not the behaviour under test.
//
// The stub is faithful in the one way that matters for conformist#124: it computes
// each check's patch AT CALL TIME from the tree as it then stands, exactly as a
// codegenPatch derivation does. That is what makes the convergence test able to
// fail: a single-pass implementation applies one check's patch and then hands the
// other's now-stale patch to `git apply`.

// stubSpec describes what the stand-in nix should produce for one check. Either
// `patch` (a literal diff, for cases a tree-derived diff cannot express) or the
// `file`/`gen` pair (a generator transforming the tree's copy of one file).
type stubSpec struct {
	// name is the check's attr name under checks.<system>.
	name string
	// patch is a literal patch body, used verbatim. Wins over file/gen.
	patch string
	// file is the tree-relative path this check regenerates.
	file string
	// gen is a bash snippet rewriting "$target" (the work-tree copy of file) in
	// place. Its output is diffed against the tree's current content.
	gen string
}

// bashShebang returns a shebang naming bash by absolute path, so a stub script
// resolves its interpreter without depending on PATH inside the build sandbox.
func bashShebang(t *test_ui.T) string {
	t.Helper()

	bash, err := exec.LookPath("bash")
	require.NoError(t, err)

	return "#!" + bash + "\n"
}

// rewriteLine is a gen snippet setting line `n` of the generated file to `text`.
// The temp-file swap avoids GNU/BSD `sed -i` divergence, matching the formatter
// stubs in cmd/staged_repair_test.go.
func rewriteLine(n int, text string) string {
	return "swap=$(mktemp); sed '" + strconv.Itoa(n) + "s/.*/" + text + "/' \"$target\" > \"$swap\"; " +
		"mv \"$swap\" \"$target\""
}

// nixStub writes a stand-in `nix` covering the two invocations the engine makes:
// `nix eval … --apply` (answered with a canned discovery result) and `nix build …
// passthru.codegenPatch` (answered with a freshly computed patch). It returns the
// script path and the path of a file that gains one line per build, so a test can
// assert that a patch was REBUILT rather than reused.
func nixStub(t *test_ui.T, dir string, checks []Check, specs []stubSpec) (string, string) {
	t.Helper()

	payload, err := json.Marshal(checks)
	require.NoError(t, err)

	countFile := filepath.Join(dir, "builds")
	script := filepath.Join(dir, "nix")

	var cases strings.Builder

	for _, spec := range specs {
		cases.WriteString("  " + spec.name + ")\n")

		if spec.patch != "" {
			cases.WriteString("    cat > \"$out/patch\" <<'STUBPATCH'\n")
			cases.WriteString(spec.patch)
			cases.WriteString("STUBPATCH\n")
			cases.WriteString("    echo \"$out\"\n")
			cases.WriteString("    exit 0\n")
			cases.WriteString("    ;;\n")

			continue
		}

		cases.WriteString("    file='" + spec.file + "'\n")
		cases.WriteString("    mkdir -p \"$tmp/src/$(dirname \"$file\")\" \"$tmp/work/$(dirname \"$file\")\"\n")
		cases.WriteString("    if [ -e \"$file\" ]; then\n")
		cases.WriteString("      cp \"$file\" \"$tmp/src/$file\"\n")
		cases.WriteString("      cp \"$file\" \"$tmp/work/$file\"\n")
		cases.WriteString("    fi\n")
		cases.WriteString("    target=\"$tmp/work/$file\"\n")
		cases.WriteString("    " + spec.gen + "\n")
		cases.WriteString("    ;;\n")
	}

	body := bashShebang(t) +
		"set -euo pipefail\n" +
		"\n" +
		"if [ \"${1:-}\" = eval ]; then\n" +
		"  cat <<'STUBJSON'\n" +
		string(payload) + "\n" +
		"STUBJSON\n" +
		"  exit 0\n" +
		"fi\n" +
		"\n" +
		"echo build >> '" + countFile + "'\n" +
		"\n" +
		"attr=\"${@: -1}\"\n" +
		"rest=\"${attr#*'#checks.'}\"\n" +
		"name=\"${rest#*.}\"\n" +
		"name=\"${name%%.*}\"\n" +
		"\n" +
		"out=$(mktemp -d)\n" +
		"tmp=$(mktemp -d)\n" +
		"mkdir -p \"$tmp/src\" \"$tmp/work\"\n" +
		"\n" +
		"case \"$name\" in\n" +
		cases.String() +
		"  *)\n" +
		"    echo \"stub nix: no spec for check $name\" >&2\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n" +
		"\n" +
		"cd \"$tmp\"\n" +
		// `git diff --no-index` exits 1 when the trees differ, which is the
		// normal case here, so its status is not a failure signal.
		"'" + git.Binary + "' diff --no-index --binary src work > \"$out/patch\" || true\n" +
		"echo \"$out\"\n"

	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	return script, countFile
}

// initRepo makes dir a git worktree with one commit containing the given files,
// so `git apply` has real tracked content to patch.
func initRepo(t *test_ui.T, dir string, files map[string]string) {
	t.Helper()

	run := func(args ...string) {
		t.Helper()

		full := append([]string{"-C", dir}, args...)
		out, err := exec.CommandContext(t.Context(), git.Binary, full...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "conformist-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conformist-test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "conformist-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conformist-test@example.invalid")

	run("init", "--quiet", "--initial-branch", "main")

	for path, content := range files {
		full := filepath.Join(dir, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}

	run("add", "-A")
	run("commit", "--quiet", "-m", "initial")
}

// options returns an Options aimed at a stub nix and a test worktree. The system
// double is pinned so the test does not depend on the host's architecture.
func options(nix, treeRoot string) Options {
	return Options{
		FlakeRef: ".",
		System:   "x86_64-linux",
		Nix:      nix,
		TreeRoot: treeRoot,
	}
}

func readFile(t *test_ui.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(content)
}

func gitStatus(t *test_ui.T, dir string) string {
	t.Helper()

	out, err := exec.CommandContext(
		t.Context(), git.Binary, "-C", dir, "status", "--porcelain", "--untracked-files=all",
	).Output()
	require.NoError(t, err)

	return strings.TrimSpace(string(out))
}

// TestConvergeAppliesOverlappingChecks pins igloo#80 note 3, the reason this
// engine loops at all: two checks that rewrite the SAME file each produce a patch
// against their own `src`, so the second one's patch does not apply once the first
// has landed. Only a rebuild against the updated tree does. `alpha` owns line 1
// and `beta` owns line 2 of one generated file; a correct run ends with both
// lines regenerated, and a single-pass run cannot get there.
func TestConvergeAppliesOverlappingChecks(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	initRepo(t, dir, map[string]string{"gen.txt": "stale\nstale\n"})

	nix, counts := nixStub(t, aux,
		[]Check{{Name: "alpha"}, {Name: "beta"}},
		[]stubSpec{
			{name: "alpha", file: "gen.txt", gen: rewriteLine(1, "alpha")},
			{name: "beta", file: "gen.txt", gen: rewriteLine(2, "beta")},
		},
	)

	as.NoError(Run(context.Background(), options(nix, dir)))

	as.Equal("alpha\nbeta\n", readFile(t, filepath.Join(dir, "gen.txt")))

	// Five builds: alpha+beta on pass 1, alpha(empty)+beta on pass 2,
	// alpha(empty)... the exact count is an implementation detail, but it must
	// exceed the two a single pass would do — that is the rebuild.
	builds := strings.Count(readFile(t, counts), "build")
	as.Greater(builds, 2, "each remaining patch must be rebuilt after one is applied")
}

// TestConvergeRefusesToLoopForever pins the iteration bound: two generators that
// each overwrite the other's output never converge, and must fail where an
// operator can see it rather than spin. Exit code 2 (ErrRepairFailed), not a
// silent warning — a repo in this state needs a human.
func TestConvergeRefusesToLoopForever(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	initRepo(t, dir, map[string]string{"gen.txt": "stale\n"})

	// Both checks claim line 1, so whichever applied last is undone by the other.
	nix, _ := nixStub(t, aux,
		[]Check{{Name: "alpha"}, {Name: "beta"}},
		[]stubSpec{
			{name: "alpha", file: "gen.txt", gen: rewriteLine(1, "alpha")},
			{name: "beta", file: "gen.txt", gen: rewriteLine(1, "beta")},
		},
	)

	err := Run(context.Background(), options(nix, dir))

	as.ErrorIs(err, ErrRepairFailed)
	as.Contains(err.Error(), "did not converge")
}

// subdirPatch is a modify-hunk rooted at a module whose src is a repo
// SUBDIRECTORY: after the -p2 strip its target is `config_tommy.go`, which is the
// path relative to that module root, not to the repository.
const subdirPatch = `diff --git a/src/config_tommy.go b/work/config_tommy.go
--- a/src/config_tommy.go
+++ b/work/config_tommy.go
@@ -1 +1 @@
-package stale
+package fresh
`

// TestSubdirectoryRootedPatchIsRefusedWithDiagnosis pins the interim behaviour for
// igloo#80 note 2 while the contract carries no codegenPrefix: a patch rooted at a
// module subdirectory is REFUSED, naming the missing path and the field that would
// fix it. The refusal is the point — applying it would have written
// `config_tommy.go` at the repository root, which is a silent wrong-prefix write
// and the worst available outcome.
func TestSubdirectoryRootedPatchIsRefusedWithDiagnosis(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	// The real file lives under go/; nothing by that name exists at the root.
	initRepo(t, dir, map[string]string{"go/config_tommy.go": "package stale\n"})

	nix, _ := nixStub(t, aux,
		[]Check{{Name: "tommy-codegen"}},
		[]stubSpec{{name: "tommy-codegen", patch: subdirPatch}},
	)

	err := Run(context.Background(), options(nix, dir))

	as.ErrorIs(err, ErrRepairFailed)
	as.Contains(err.Error(), "codegenPrefix")
	as.Contains(err.Error(), "config_tommy.go")

	// Nothing was written at the wrong prefix, and the real file is untouched.
	as.NoFileExists(filepath.Join(dir, "config_tommy.go"))
	as.Equal("package stale\n", readFile(t, filepath.Join(dir, "go", "config_tommy.go")))
}

// TestPrefixPlacesPatchUnderModuleRoot pins the forward-compatible half of the
// same note: once a check publishes passthru.codegenPrefix, the very patch refused
// above lands in the right subdirectory, with no other change to this engine. This
// is what lets igloo add the field later without a conformist release.
func TestPrefixPlacesPatchUnderModuleRoot(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	initRepo(t, dir, map[string]string{"go/config_tommy.go": "package stale\n"})

	nix, _ := nixStub(t, aux,
		[]Check{{Name: "tommy-codegen", Prefix: "go"}},
		[]stubSpec{{name: "tommy-codegen", patch: subdirPatch}},
	)

	as.NoError(Run(context.Background(), options(nix, dir)))

	as.Equal("package fresh\n", readFile(t, filepath.Join(dir, "go", "config_tommy.go")))
	as.NoFileExists(filepath.Join(dir, "config_tommy.go"))
}

// TestCreatedOutputLeavesIndexUntouched pins the interaction pennywise asked to be
// settled by test. A patch that CREATES a file is recorded with
// `git add --intent-to-add` so the next pass's nix build can see it (nix resolves
// a dirty flake from tracked paths only) — but that entry must not survive the
// run: staging a generated file is the --staged/--commit hook's decision
// (conformist#56's stage-new-outputs opt-in), and an intent-to-add entry left
// behind would commit as an EMPTY file for a caller who never opted in. So the new
// file must end up on disk and still be plain untracked ("??"), not staged ("A").
func TestCreatedOutputLeavesIndexUntouched(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	initRepo(t, dir, map[string]string{"seed.txt": "seed\n"})

	// The generated file does not exist yet, so the diff is a pure creation.
	nix, _ := nixStub(t, aux,
		[]Check{{Name: "facade"}},
		[]stubSpec{{
			name: "facade",
			file: "generated/facade.txt",
			gen:  "printf 'facade\\n' > \"$target\"",
		}},
	)

	as.NoError(Run(context.Background(), options(nix, dir)))

	as.Equal("facade\n", readFile(t, filepath.Join(dir, "generated", "facade.txt")))
	as.Equal("?? generated/facade.txt", gitStatus(t, dir))
}

// TestDiscoveryFailsSoftUnlessStrict pins the fail-soft policy: this runs inside a
// git pre-commit hook, where a non-zero exit blocks the commit, so an unreachable
// nix must not stop a repo from committing — the drift check still catches the
// staleness. --strict and --list both want the error instead.
func TestDiscoveryFailsSoftUnlessStrict(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	initRepo(t, dir, map[string]string{"seed.txt": "seed\n"})

	broken := filepath.Join(aux, "nix-broken")
	as.NoError(os.WriteFile(
		broken,
		[]byte(bashShebang(t)+"echo 'nix: no flake here' >&2\nexit 1\n"),
		0o755,
	))

	opts := options(broken, dir)

	as.NoError(Run(context.Background(), opts), "a broken nix must not block the commit")

	strict := opts
	strict.Strict = true
	as.ErrorIs(Run(context.Background(), strict), ErrRepairFailed)

	// --list is a diagnostic (and the eval-cost measurement's subject), so it
	// reports the failure even without --strict.
	listing := opts
	listing.ListOnly = true
	listing.Stdout = &strings.Builder{}
	as.ErrorIs(Run(context.Background(), listing), ErrRepairFailed)
}

// TestDiscoverParsesContractFields pins the shape of the discovery result: the
// engine reads name, prefix and includes, and sorts by name so a run's order and
// log are deterministic.
func TestDiscoverParsesContractFields(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	initRepo(t, dir, map[string]string{"seed.txt": "seed\n"})

	nix, _ := nixStub(t, aux,
		[]Check{
			{Name: "zulu", Includes: []string{"flake.lock"}},
			{Name: "alpha", Prefix: "go", Includes: []string{"*.go", "go.nix"}},
		},
		nil,
	)

	checks, err := Discover(context.Background(), options(nix, dir))
	as.NoError(err)

	as.Equal(
		[]Check{
			{Name: "alpha", Prefix: "go", Includes: []string{"*.go", "go.nix"}},
			{Name: "zulu", Includes: []string{"flake.lock"}},
		},
		checks,
	)
}

// TestMissingTargetsUsesGitsOwnStrip pins the git behaviour the subdirectory
// diagnosis rests on: `git apply --numstat` reports each path with the -p2 strip
// already applied. If that ever stopped holding, the diagnosis would silently name
// the wrong paths — the class of defect this repo treats as worse than no check at
// all — so it is asserted directly rather than inferred from a passing diagnosis.
func TestMissingTargetsUsesGitsOwnStrip(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	// present.txt exists at the root; config_tommy.go does not.
	initRepo(t, dir, map[string]string{"present.txt": "here\n"})

	patch := `diff --git a/src/present.txt b/work/present.txt
--- a/src/present.txt
+++ b/work/present.txt
@@ -1 +1 @@
-here
+there
` + subdirPatch

	patchPath := filepath.Join(aux, "patch")
	as.NoError(os.WriteFile(patchPath, []byte(patch), 0o644))

	s := &session{opts: options("nix", dir)}

	as.Equal(
		[]string{"config_tommy.go"},
		s.missingTargets(context.Background(), Check{Name: "probe"}, patchPath),
	)
}

// createHunk creates `fresh.txt`, a file that legitimately does not exist in the
// tree. Its absence must NOT be read as evidence of a wrong patch root.
const createHunk = `diff --git a/src/fresh.txt b/work/fresh.txt
new file mode 100644
--- /dev/null
+++ b/work/fresh.txt
@@ -0,0 +1 @@
+fresh
`

// TestMissingTargetsExcludesCreatedFiles pins the fix for the wrong-prefix
// diagnosis over-reporting. A creation hunk's target is absent BY DEFINITION, so
// counting it as missing would make any patch containing a new file look
// subdirectory-rooted. `git apply --summary` is asked which paths the patch
// creates, and those are excluded.
func TestMissingTargetsExcludesCreatedFiles(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	initRepo(t, dir, map[string]string{"present.txt": "here\n"})

	// present.txt exists; fresh.txt is created by the patch; config_tommy.go is
	// neither — it is the genuinely wrong-rooted one.
	patch := `diff --git a/src/present.txt b/work/present.txt
--- a/src/present.txt
+++ b/work/present.txt
@@ -1 +1 @@
-here
+there
` + createHunk + subdirPatch

	patchPath := filepath.Join(aux, "patch")
	as.NoError(os.WriteFile(patchPath, []byte(patch), 0o644))

	s := &session{opts: options("nix", dir)}

	as.Equal(
		[]string{"config_tommy.go"},
		s.missingTargets(context.Background(), Check{Name: "probe"}, patchPath),
	)
}

// TestConflictWithNewFileGetsGenericRefusal is the same fix seen from the outside:
// a root-rooted check whose patch genuinely conflicts must be reported as a
// conflict, even when the patch also creates a file. Diagnosing it as a
// subdirectory-module problem would send the operator after a module root that does
// not exist — a confidently wrong diagnosis, which this repo treats as worse than
// the honest ambiguous one.
func TestConflictWithNewFileGetsGenericRefusal(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	// The tree's content does not match what the patch's hunk expects, so neither
	// the forward nor the reverse --check can apply: a real conflict.
	initRepo(t, dir, map[string]string{"a.txt": "actual\n"})

	conflicting := `diff --git a/src/a.txt b/work/a.txt
--- a/src/a.txt
+++ b/work/a.txt
@@ -1 +1 @@
-expected
+rewritten
` + createHunk

	nix, _ := nixStub(t, aux,
		[]Check{{Name: "rooted"}},
		[]stubSpec{{name: "rooted", patch: conflicting}},
	)

	err := Run(context.Background(), options(nix, dir))

	as.ErrorIs(err, ErrRepairFailed)
	as.Contains(err.Error(), "changed under the build")
	as.NotContains(err.Error(), "codegenPrefix")

	// Nothing was applied, including the creation hunk.
	as.Equal("actual\n", readFile(t, filepath.Join(dir, "a.txt")))
	as.NoFileExists(filepath.Join(dir, "fresh.txt"))
}

// TestResolveRefusesAmbiguousFlakeRoot pins the fix for the nested-flake hazard.
// nix resolves `.#checks` from the directory it runs in, and conformist invokes a
// whole-tree repair with the cwd set to ITS tree root — which a repo may point
// somewhere other than the git toplevel. Silently preferring the toplevel would
// enumerate a different flake than the linter's own `[ -f flake.nix ]` gate
// checked, and apply its patches at a different root.
func TestResolveRefusesAmbiguousFlakeRoot(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	initRepo(t, dir, map[string]string{"nested/flake.nix": "{ outputs = _: { }; }\n"})

	// Stand where a nested flake would put conformist's tree root.
	t.Chdir(filepath.Join(dir, "nested"))

	opts := Options{System: "x86_64-linux", Nix: "nix"}
	err := opts.Resolve()

	as.ErrorIs(err, ErrRepairFailed)
	as.Contains(err.Error(), "ambiguous")
	as.Contains(err.Error(), "--tree-root")

	// An explicit --tree-root resolves it rather than being second-guessed.
	explicit := Options{System: "x86_64-linux", Nix: "nix", TreeRoot: dir}
	as.NoError(explicit.Resolve())
	as.Equal(dir, explicit.TreeRoot)
}

// TestNonConvergenceNamesWhatItApplied pins that the give-up path says what it
// already wrote. That path leaves the tree mutated by every patch that landed
// before the bound was hit, and nothing reverts them, so the names are the only
// handle anyone has on the damage.
func TestNonConvergenceNamesWhatItApplied(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	dir := t.TempDir()
	aux := t.TempDir()

	initRepo(t, dir, map[string]string{"gen.txt": "stale\n"})

	nix, _ := nixStub(t, aux,
		[]Check{{Name: "alpha"}, {Name: "beta"}},
		[]stubSpec{
			{name: "alpha", file: "gen.txt", gen: rewriteLine(1, "alpha")},
			{name: "beta", file: "gen.txt", gen: rewriteLine(1, "beta")},
		},
	)

	err := Run(context.Background(), options(nix, dir))

	as.ErrorIs(err, ErrRepairFailed)
	as.Contains(err.Error(), "alpha")
	as.Contains(err.Error(), "beta")
	as.Contains(err.Error(), "Nothing was reverted")
}

// TestCheckValidateRejectsUnsafePrefix pins the guard on codegenPrefix: it becomes
// a `git apply --directory` argument, so an absolute or climbing value would write
// outside the tree. A prefix this engine does not understand is a contract
// mismatch, and sanitizing one is how a repair tool corrupts a repository.
func TestCheckValidateRejectsUnsafePrefix(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	as.NoError(Check{Name: "ok"}.validate())
	as.NoError(Check{Name: "ok", Prefix: "go"}.validate())
	as.NoError(Check{Name: "ok", Prefix: "nested/module"}.validate())

	as.ErrorContains(Check{Name: "ok", Prefix: "/etc"}.validate(), "absolute")
	as.ErrorContains(Check{Name: "ok", Prefix: "../sibling"}.validate(), "climbs out")
	as.ErrorContains(Check{Name: "has space"}.validate(), "unquoted")
}
