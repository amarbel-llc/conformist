package format_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/format"
	"github.com/amarbel-llc/conformist/stats"
	"github.com/amarbel-llc/conformist/walk"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

func writeFile(t *test_ui.T, root, rel, content string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), mode))

	return path
}

func walkFile(t *test_ui.T, root, rel string) *walk.File {
	t.Helper()

	info, err := os.Stat(filepath.Join(root, rel))
	require.NoError(t, err)

	return &walk.File{Path: filepath.Join(root, rel), RelPath: rel, Info: info}
}

// TestCompositeCheckerLinterFindings verifies that a linter's non-zero exit is
// surfaced as a lint finding and a clean run is not.
func TestCompositeCheckerLinterFindings(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	// stub linter: exit 1 if any passed file contains the marker "BAD".
	lint := writeFile(t, root, "lint.sh",
		"#!/usr/bin/env bash\nfor f in \"$@\"; do grep -q BAD \"$f\" && exit 1; done\nexit 0\n", 0o755)

	writeFile(t, root, "good.sh", "echo ok\n", 0o644)
	writeFile(t, root, "bad.sh", "echo BAD\n", 0o644)

	statz := stats.New()

	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		LinterConfigs: map[string]*config.Linter{
			"stub": {Command: lint, Includes: []string{"*.sh"}},
		},
	}

	checker, err := format.NewCompositeChecker(cfg, &statz)
	as.NoError(err)

	findings, err := checker.Check(context.Background(), []*walk.File{
		walkFile(t, root, "good.sh"),
		walkFile(t, root, "bad.sh"),
	})
	as.NoError(err)
	as.Len(findings, 1)
	as.Equal(format.FindingLint, findings[0].Kind)
	as.Equal("stub", findings[0].Tool)
}

// TestCompositeCheckerWholeTreeLinter verifies that a linter with
// passes-files=false runs exactly once with no file arguments (a whole-tree
// check), gated on at least one of its included files being present.
// See amarbel-llc/conformist#1.
func TestCompositeCheckerWholeTreeLinter(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	// stub whole-tree check: records each invocation's arg count to runs.log and
	// exits non-zero if it is ever handed a file argument.
	lint := writeFile(t, root, "check.sh",
		"#!/usr/bin/env bash\necho \"args=$#\" >> runs.log\n[ \"$#\" -eq 0 ] || exit 2\nexit 0\n", 0o755)

	writeFile(t, root, "a.go", "package a\n", 0o644)
	writeFile(t, root, "b.go", "package b\n", 0o644)

	statz := stats.New()

	passesFiles := false
	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		LinterConfigs: map[string]*config.Linter{
			"whole": {
				Command:     lint,
				Includes:    []string{"*.go"},
				PassesFiles: &passesFiles,
			},
		},
	}

	checker, err := format.NewCompositeChecker(cfg, &statz)
	as.NoError(err)

	findings, err := checker.Check(context.Background(), []*walk.File{
		walkFile(t, root, "a.go"),
		walkFile(t, root, "b.go"),
	})
	as.NoError(err)

	// whole-tree checks accumulate during Check and run in Finalize (no cache db
	// is set here, so the check always runs).
	wholeFindings, err := checker.Finalize(context.Background())
	as.NoError(err)
	findings = append(findings, wholeFindings...)
	as.Empty(findings, "a clean whole-tree check should report no findings")

	// the check must have run exactly once, with zero file arguments
	runs, err := os.ReadFile(filepath.Join(root, "runs.log"))
	as.NoError(err)
	as.Equal("args=0\n", string(runs))
}

// TestLinterWorkingDir verifies a whole-tree linter with working-dir runs in
// that subdirectory (conformist#38): the check is clean only when run from the
// subdir holding its marker file, otherwise it reports a finding.
func TestLinterWorkingDir(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	// stub whole-tree check: clean (exit 0) iff a `marker` file is in the cwd.
	lint := writeFile(t, root, "cwdcheck.sh",
		"#!/usr/bin/env bash\n[ -f marker ]\n", 0o755)

	writeFile(t, root, "sub/marker", "", 0o644)
	writeFile(t, root, "sub/x.go", "package x\n", 0o644)

	passesFiles := false
	run := func(workingDir string) []format.Finding {
		t.Helper()

		statz := stats.New()
		cfg := &config.Config{
			TreeRoot:    root,
			OnUnmatched: "info",
			LinterConfigs: map[string]*config.Linter{
				"cwd": {
					Command:     lint,
					Includes:    []string{"sub/*.go"},
					PassesFiles: &passesFiles,
					WorkingDir:  workingDir,
				},
			},
		}

		checker, err := format.NewCompositeChecker(cfg, &statz)
		as.NoError(err)

		_, err = checker.Check(context.Background(), []*walk.File{walkFile(t, root, "sub/x.go")})
		as.NoError(err)

		findings, err := checker.Finalize(context.Background())
		as.NoError(err)

		return findings
	}

	as.Empty(run("sub"), "working-dir=sub should run the check from sub/, where marker lives")
	as.Len(run(""), 1, "without working-dir the check runs at the tree root, where marker is absent")
}

// TestLinterWorkingDirFileArgs verifies that with working-dir set, matched file
// paths are passed RELATIVE to that subdirectory so the tool resolves them from
// its cwd (conformist#38).
func TestLinterWorkingDirFileArgs(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	// stub linter: exit non-zero unless every arg resolves as a file from the cwd.
	lint := writeFile(t, root, "argcheck.sh",
		"#!/usr/bin/env bash\nfor f in \"$@\"; do [ -f \"$f\" ] || exit 3; done\nexit 0\n", 0o755)

	writeFile(t, root, "sub/a.go", "package a\n", 0o644)

	statz := stats.New()
	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		LinterConfigs: map[string]*config.Linter{
			"args": {Command: lint, Includes: []string{"sub/*.go"}, WorkingDir: "sub"},
		},
	}

	checker, err := format.NewCompositeChecker(cfg, &statz)
	as.NoError(err)

	findings, err := checker.Check(context.Background(), []*walk.File{walkFile(t, root, "sub/a.go")})
	as.NoError(err)
	as.Empty(findings, "the file arg must be passed relative to sub/ (a.go), so the tool resolves it from its cwd")
}

// TestFormatterWorkingDirSandbox verifies working-dir is honored in the sandbox
// check path (conformist#38): the formatter runs in the sandbox copy of its
// subdir with file args relative to it. The stub only acts on a slash-free arg,
// so it modifies (and is reported) only when the path was relocated to "a.txt".
func TestFormatterWorkingDirSandbox(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	// fix-only formatter: append to each arg that has NO slash (i.e. was passed
	// relative to the working-dir), leaving tree-relative "src/a.txt" untouched.
	fix := writeFile(t, root, "fix.sh",
		"#!/usr/bin/env bash\nfor f in \"$@\"; do case \"$f\" in */*) ;; *) printf 'X' >> \"$f\";; esac; done\nexit 0\n",
		0o755)

	writeFile(t, root, "src/a.txt", "hello", 0o644)

	run := func(workingDir string) []format.Finding {
		t.Helper()

		statz := stats.New()
		cfg := &config.Config{
			TreeRoot:    root,
			OnUnmatched: "info",
			FormatterConfigs: map[string]*config.Formatter{
				"stub": {Command: fix, Includes: []string{"*.txt"}, WorkingDir: workingDir},
			},
		}

		checker, err := format.NewCompositeChecker(cfg, &statz)
		as.NoError(err)

		findings, err := checker.Check(context.Background(), []*walk.File{walkFile(t, root, "src/a.txt")})
		as.NoError(err)

		// the source is never written by a check, regardless of working-dir
		after, err := os.ReadFile(filepath.Join(root, "src/a.txt"))
		as.NoError(err)
		as.Equal("hello", string(after))

		return findings
	}

	withWD := run("src")
	as.Len(withWD, 1, "working-dir=src relocates the arg to a slash-free a.txt, which the stub modifies")
	as.Equal("src/a.txt", withWD[0].Path)

	as.Empty(run(""), "without working-dir the arg is src/a.txt (has a slash); the stub is a no-op")
}

// TestCommandShellDetection pins the bare-command-vs-shell-line routing (#38)
// through the public API: a single literal word is PATH-resolved at construction
// (so an unresolved one fails with ErrCommandNotFound), while anything with shell
// syntax — operators, pipes, assignments, multiple words — is never resolved at
// construction (it is run through the interpreter, failing only at run time, if
// at all).
func TestCommandShellDetection(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	build := func(command string, assert func(error)) {
		t.Helper()

		statz := stats.New()
		cfg := &config.Config{
			TreeRoot:    t.TempDir(),
			OnUnmatched: "info",
			// RequireTools keeps NewCompositeFormatter strict so an unresolved bare
			// command still surfaces ErrCommandNotFound here — this test pins the
			// bare-vs-shell resolution routing (#38), not the repair-mode degrade
			// behavior (conformist#75), which would otherwise swallow the error.
			RequireTools: true,
			FormatterConfigs: map[string]*config.Formatter{
				"t": {Command: command, Includes: []string{"*"}},
			},
		}

		_, err := format.NewCompositeFormatter(cfg, &statz, 1024)
		assert(err)
	}

	// A bare, unresolved command is looked up on PATH at construction and fails.
	build("definitely-not-a-real-binary-xyzzy", func(err error) {
		as.ErrorIs(err, format.ErrCommandNotFound)
	})

	// Shell lines are not PATH-resolved at construction, so they build fine even
	// though `definitely-not-a-real-binary-xyzzy` does not exist.
	for _, command := range []string{
		"definitely-not-a-real-binary-xyzzy && true",
		"definitely-not-a-real-binary-xyzzy | cat",
		"FOO=1 definitely-not-a-real-binary-xyzzy",
		"definitely-not-a-real-binary-xyzzy arg1 arg2",
		"cd sub && definitely-not-a-real-binary-xyzzy",
	} {
		build(command, func(err error) {
			as.NoError(err, "a shell line must not be PATH-resolved at construction: %q", command)
		})
	}
}

// TestLinterShellCommand verifies a linter whose `command` is a shell line (it
// uses `&&`/`||`, so it runs through the interpreter, not a bare exec) reports a
// non-zero exit as findings rather than an operational error (conformist#38).
func TestLinterShellCommand(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	writeFile(t, root, "good.sh", "echo ok\n", 0o644)
	writeFile(t, root, "bad.sh", "echo BAD\n", 0o644)

	statz := stats.New()
	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		LinterConfigs: map[string]*config.Linter{
			// shell line: exit 1 (findings) if any passed file ($@) contains BAD.
			"shell": {Command: `grep -q BAD "$@" && exit 1 || exit 0`, Includes: []string{"*.sh"}},
		},
	}

	checker, err := format.NewCompositeChecker(cfg, &statz)
	as.NoError(err)

	findings, err := checker.Check(context.Background(), []*walk.File{
		walkFile(t, root, "good.sh"),
		walkFile(t, root, "bad.sh"),
	})
	as.NoError(err, "a non-zero exit from the shell line is findings, not an operational error")
	as.Len(findings, 1)
	as.Equal(format.FindingLint, findings[0].Kind)
}

// TestLinterShellRepairWorkingDir verifies the motivating #38 case: a whole-tree
// repair-command that is a shell line, run in a subdirectory via working-dir.
// The repair writes its output only when run from the subdir holding its marker.
func TestLinterShellRepairWorkingDir(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	writeFile(t, root, "sub/x.go", "package x\n", 0o644)
	writeFile(t, root, "sub/marker", "", 0o644) // present only under sub/

	passesFiles := false
	statz := stats.New()
	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		LinterConfigs: map[string]*config.Linter{
			"gen": {
				Command: "true", // bare no-op check
				// shell line (`&&` + redirect): writes out.txt iff marker is in cwd.
				RepairCommand: `[ -f marker ] && printf gen > out.txt`,
				Includes:      []string{"sub/*.go"},
				PassesFiles:   &passesFiles,
				WorkingDir:    "sub",
			},
		},
	}

	linter, err := format.NewCompositeLinter(cfg, &statz)
	as.NoError(err)

	as.NoError(linter.Repair(context.Background(), []*walk.File{walkFile(t, root, "sub/x.go")}))

	// the repair ran in sub/ (where marker lives), writing sub/out.txt
	out, err := os.ReadFile(filepath.Join(root, "sub", "out.txt"))
	as.NoError(err)
	as.Equal("gen", string(out))

	// and nothing was written at the tree root
	_, err = os.Stat(filepath.Join(root, "out.txt"))
	as.True(os.IsNotExist(err), "the repair must not run at the tree root")
}

// TestFormatterShellCommand verifies a formatter whose `command` is a shell line
// (a `for` loop) runs through the interpreter and is checked via the sandbox
// (conformist#38): the change it would make is reported, the source untouched.
func TestFormatterShellCommand(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	writeFile(t, root, "a.txt", "hello", 0o644)

	statz := stats.New()
	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		FormatterConfigs: map[string]*config.Formatter{
			// shell line: append X to each passed file.
			"shell": {Command: `for f in "$@"; do printf X >> "$f"; done`, Includes: []string{"*.txt"}},
		},
	}

	checker, err := format.NewCompositeChecker(cfg, &statz)
	as.NoError(err)

	findings, err := checker.Check(context.Background(), []*walk.File{walkFile(t, root, "a.txt")})
	as.NoError(err)
	as.Len(findings, 1)
	as.Equal(format.FindingFormat, findings[0].Kind)
	as.Equal("a.txt", findings[0].Path)

	after, err := os.ReadFile(filepath.Join(root, "a.txt"))
	as.NoError(err)
	as.Equal("hello", string(after), "the sandbox check must never write the source")
}

// TestCompositeCheckerSandbox verifies that a fix-only formatter is checked via
// the sandbox: a file that would change is reported, and the original is never
// modified on disk.
func TestCompositeCheckerSandbox(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	// stub fix-only formatter: append a trailing newline if one is missing.
	// The explicit `exit 0` keeps the script's status zero even when the last
	// file needs no change (otherwise the trailing test would set status 1).
	fix := writeFile(t, root, "fix.sh",
		"#!/usr/bin/env bash\nfor f in \"$@\"; do [ -n \"$(tail -c1 \"$f\")\" ] && printf '\\n' >> \"$f\"; done\nexit 0\n", 0o755)

	writeFile(t, root, "needs.txt", "no-trailing-newline", 0o644)
	writeFile(t, root, "ok.txt", "already-fine\n", 0o644)

	statz := stats.New()

	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		FormatterConfigs: map[string]*config.Formatter{
			"stub": {Command: fix, Includes: []string{"*.txt"}},
		},
	}

	checker, err := format.NewCompositeChecker(cfg, &statz)
	as.NoError(err)

	before, err := os.ReadFile(filepath.Join(root, "needs.txt"))
	as.NoError(err)

	findings, err := checker.Check(context.Background(), []*walk.File{
		walkFile(t, root, "needs.txt"),
		walkFile(t, root, "ok.txt"),
	})
	as.NoError(err)

	as.Len(findings, 1)
	as.Equal(format.FindingFormat, findings[0].Kind)
	as.Equal("needs.txt", findings[0].Path)

	// the sandbox must never write the original file
	after, err := os.ReadFile(filepath.Join(root, "needs.txt"))
	as.NoError(err)
	as.Equal(string(before), string(after))
}

// TestCompositeCheckerSandboxConfigFiles verifies that a formatter's declared
// config-files are shipped into the sandbox from the matched file's ancestor
// directories, so a fix-only formatter discovers the same config in check mode
// as in repair mode (conformist#28). Without config-files the sandbox runs the
// tool with default behaviour and disagrees with the real tree — the guard
// sub-case asserts exactly that, proving the mechanism is what carries the
// config in.
func TestCompositeCheckerSandboxConfigFiles(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	// stub fix-only formatter: uppercases each file only when a `cfg.ini` in its
	// CWD (the sandbox root) declares `mode=upper`. With no cfg.ini it is a
	// no-op, mirroring a real tool falling back to its default config.
	const fixScript = "#!/usr/bin/env bash\n" +
		"mode=\"\"\n" +
		"[ -f cfg.ini ] && mode=$(sed -n 's/^mode=//p' cfg.ini)\n" +
		"for f in \"$@\"; do\n" +
		"  if [ \"$mode\" = upper ]; then tr '[:lower:]' '[:upper:]' < \"$f\" > \"$f.tmp\" && mv \"$f.tmp\" \"$f\"; fi\n" +
		"done\n" +
		"exit 0\n"

	run := func(configFiles []string) []format.Finding {
		t.Helper()
		root := t.TempDir()

		fix := writeFile(t, root, "fix.sh", fixScript, 0o755)
		// config lives at the tree root, an ancestor of the matched file.
		writeFile(t, root, "cfg.ini", "mode=upper\n", 0o644)
		writeFile(t, root, "src/a.txt", "hello", 0o644)

		statz := stats.New()
		cfg := &config.Config{
			TreeRoot:    root,
			OnUnmatched: "info",
			FormatterConfigs: map[string]*config.Formatter{
				"stub": {Command: fix, Includes: []string{"*.txt"}, ConfigFiles: configFiles},
			},
		}

		checker, err := format.NewCompositeChecker(cfg, &statz)
		as.NoError(err)

		findings, err := checker.Check(context.Background(), []*walk.File{
			walkFile(t, root, "src/a.txt"),
		})
		as.NoError(err)

		// the source must never be written by a check
		after, err := os.ReadFile(filepath.Join(root, "src/a.txt"))
		as.NoError(err)
		as.Equal("hello", string(after))

		return findings
	}

	// With config-files declared, cfg.ini reaches the sandbox; the formatter
	// uppercases and the file is reported as needing formatting.
	withConfig := run([]string{"cfg.ini"})
	as.Len(withConfig, 1)
	as.Equal(format.FindingFormat, withConfig[0].Kind)
	as.Equal("src/a.txt", withConfig[0].Path)

	// Without config-files, cfg.ini is absent in the sandbox; the formatter is a
	// no-op and (incorrectly, vs. the real tree) reports nothing. This is the
	// pre-fix bug behaviour the config-files mechanism corrects.
	withoutConfig := run(nil)
	as.Empty(withoutConfig)
}

// TestCompositeCheckerSandboxReadOnlySource is a regression test for the
// writable-sandbox-copy fix (commit e58928e, issue #3): a read-only source
// (mode 0444, e.g. a /nix/store path under `nix flake check`) must still be
// checkable by a fix-only formatter. copyIntoSandbox forces owner read+write on
// the copy so the formatter rewrites it in place; the original is never touched.
func TestCompositeCheckerSandboxReadOnlySource(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	// stub fix-only formatter: unconditionally append a newline. If the sandbox
	// copy is read-only the `>>` fails and the script exits non-zero, which is
	// exactly how a real fix-only formatter (gofumpt -w, …) reports the denial.
	fix := writeFile(t, root, "fix.sh",
		"#!/usr/bin/env bash\nfor f in \"$@\"; do printf '\\n' >> \"$f\"; done\n", 0o755)

	// read-only source that needs formatting
	const want = "no-trailing-newline"
	src := writeFile(t, root, "needs.txt", want, 0o444)

	statz := stats.New()

	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		FormatterConfigs: map[string]*config.Formatter{
			"stub": {Command: fix, Includes: []string{"*.txt"}},
		},
	}

	checker, err := format.NewCompositeChecker(cfg, &statz)
	as.NoError(err)

	// pre-fix this errored with "permission denied" because the sandbox copy
	// inherited the source's read-only mode.
	findings, err := checker.Check(context.Background(), []*walk.File{
		walkFile(t, root, "needs.txt"),
	})
	as.NoError(err)

	as.Len(findings, 1)
	as.Equal(format.FindingFormat, findings[0].Kind)
	as.Equal("needs.txt", findings[0].Path)

	// the source must be untouched: same content and still read-only
	after, err := os.ReadFile(src)
	as.NoError(err)
	as.Equal(want, string(after))

	info, err := os.Stat(src)
	as.NoError(err)
	as.Equal(os.FileMode(0o444), info.Mode().Perm())
}

// TestLinterGlobalExcludesByPassesFiles verifies the conformist#45 model: a
// globally-excluded file (e.g. go.mod, which formatters must never rewrite) is
// watched by a WHOLE-TREE check (passes-files=false), whose includes are only a
// trigger gate, but stays invisible to a PER-FILE linter (passes-files=true),
// which would receive the file as input. A formatter never sees it either way.
// This replaces the conformist#44 ignore-global-excludes flag with the
// passes-files distinction.
func TestLinterGlobalExcludesByPassesFiles(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	// stub linter: exit 1 (a finding) whenever it is run at all, so "did it run"
	// is observable as a finding.
	lint := writeFile(t, root, "ran.sh", "#!/usr/bin/env bash\nexit 1\n", 0o755)
	// fix-only formatter: append X to each arg. If a formatter ever saw the
	// excluded file the source would change; a check reports it via the sandbox.
	fix := writeFile(t, root, "fix.sh",
		"#!/usr/bin/env bash\nfor f in \"$@\"; do printf X >> \"$f\"; done\nexit 0\n", 0o755)

	writeFile(t, root, "go.mod", "module example.com/x\n", 0o644)

	// run with passesFiles=false => whole-tree check (trigger gate, exempt from
	// global excludes); passesFiles=true => per-file linter (honors them).
	run := func(passesFiles bool) []format.Finding {
		t.Helper()

		statz := stats.New()
		cfg := &config.Config{
			TreeRoot:    root,
			OnUnmatched: "info",
			Excludes:    []string{"go.mod"}, // the formatter "don't rewrite" set
			LinterConfigs: map[string]*config.Linter{
				"watch": {
					Command:     lint,
					Includes:    []string{"go.mod"},
					PassesFiles: &passesFiles,
				},
			},
			FormatterConfigs: map[string]*config.Formatter{
				// includes go.mod, but the global exclude must keep it away.
				"fmt": {Command: fix, Includes: []string{"go.mod"}},
			},
		}

		checker, err := format.NewCompositeChecker(cfg, &statz)
		as.NoError(err)

		findings, err := checker.Check(context.Background(), []*walk.File{walkFile(t, root, "go.mod")})
		as.NoError(err)

		wholeFindings, err := checker.Finalize(context.Background())
		as.NoError(err)
		findings = append(findings, wholeFindings...)

		// the formatter must never have run against the excluded file, regardless
		// of the linter kind: the source is untouched.
		after, err := os.ReadFile(filepath.Join(root, "go.mod"))
		as.NoError(err)
		as.Equal("module example.com/x\n", string(after), "a formatter must never touch a globally-excluded file")

		return findings
	}

	// Whole-tree check: watches the excluded go.mod and runs (reporting a finding).
	wholeTree := run(false)
	as.Len(wholeTree, 1, "a whole-tree check (passes-files=false) watches the globally-excluded go.mod")
	as.Equal(format.FindingLint, wholeTree[0].Kind)
	as.Equal("watch", wholeTree[0].Tool)

	// Per-file linter: the globally-excluded go.mod is invisible to it (it would
	// be fed the file as input), so the check never runs (no findings).
	as.Empty(run(true), "a per-file linter (passes-files=true) honors the global excludes")
}
