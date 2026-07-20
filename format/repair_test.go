package format_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/format"
	"code.linenisgreat.com/conformist/stats"
	"code.linenisgreat.com/conformist/walk"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// TestCompositeLinterRepair verifies that a linter with a repair command applies
// its autofix to matched files.
func TestCompositeLinterRepair(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	// stub repair tool: rewrite the marker "BAD" to "GOOD" in each file.
	fix := writeFile(t, root, "fix.sh",
		"#!/usr/bin/env bash\nfor f in \"$@\"; do sed -i 's/BAD/GOOD/g' \"$f\"; done\n", 0o755)

	writeFile(t, root, "a.sh", "echo BAD\n", 0o644)

	statz := stats.New()

	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		LinterConfigs: map[string]*config.Linter{
			// a check command is required by the schema; `true` is a no-op check.
			"stub": {Command: "true", Includes: []string{"*.sh"}, RepairCommand: fix},
		},
	}

	linter, err := format.NewCompositeLinter(cfg, &statz)
	as.NoError(err)
	as.False(linter.Empty())

	as.NoError(linter.Repair(context.Background(), []*walk.File{walkFile(t, root, "a.sh")}))

	got, err := os.ReadFile(filepath.Join(root, "a.sh"))
	as.NoError(err)
	as.Equal("echo GOOD\n", string(got))
}

// TestCompositeLinterEmptyWithoutRepair verifies that check-only linters are
// excluded from the repair-mode set (no autofix to apply).
func TestCompositeLinterEmptyWithoutRepair(t *testing.T) {
	as := require.New(t)
	root := t.TempDir()

	statz := stats.New()

	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		LinterConfigs: map[string]*config.Linter{
			"checkonly": {Command: "true", Includes: []string{"*.sh"}},
		},
	}

	linter, err := format.NewCompositeLinter(cfg, &statz)
	as.NoError(err)
	as.True(linter.Empty())
}

// TestCompositeLinterMissingRepairBinaryDegrades verifies the conformist#75
// contract: a repair-capable linter whose binary is missing from PATH must NOT
// abort repair-mode construction by default — the lane is skipped (with a loud
// warning) so a repair that only needs the other lanes still runs, the motivating
// case of a dep-bump repair dying on an unrelated missing linter binary. Setting
// RequireTools (--require-tools) restores strict failure for gates.
func TestCompositeLinterMissingRepairBinaryDegrades(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	root := t.TempDir()

	statz := stats.New()

	cfg := &config.Config{
		TreeRoot:    root,
		OnUnmatched: "info",
		LinterConfigs: map[string]*config.Linter{
			// the check command (`true`) resolves; only the repair binary is missing.
			"missing": {
				Command:       "true",
				Includes:      []string{"*.sh"},
				RepairCommand: "definitely-not-a-real-binary-xyzzy",
			},
		},
	}

	// Default: degrade. Construction succeeds and the unresolved linter is skipped,
	// leaving no repair-capable linters.
	linter, err := format.NewCompositeLinter(cfg, &statz)
	as.NoError(err)
	as.True(linter.Empty())

	// --require-tools: strict. The missing binary now aborts construction.
	cfg.RequireTools = true

	_, err = format.NewCompositeLinter(cfg, &statz)
	as.ErrorIs(err, format.ErrCommandNotFound)
}
