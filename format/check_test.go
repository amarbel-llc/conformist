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
