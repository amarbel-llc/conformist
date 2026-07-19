package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/test"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// wholeTreeStub writes an executable stub whole-tree linter that appends a line
// to `marker` on every invocation (and exits clean), returning its path. The
// marker lives outside the checked tree so it is never itself a matched file.
func wholeTreeStub(t *test_ui.T, dir, marker string) string {
	t.Helper()

	script := filepath.Join(dir, "whole.sh")
	body := "#!/usr/bin/env bash\nprintf 'ran\\n' >> '" + marker + "'\nexit 0\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	return script
}

// markerRuns counts how many times the stub linter has run (lines in marker).
func markerRuns(t *test_ui.T, marker string) int {
	t.Helper()

	data, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		return 0
	}

	require.NoError(t, err)

	return strings.Count(string(data), "ran\n")
}

// TestCheckWholeTreeCache pins amarbel-llc/conformist#16: a whole-tree
// (passes-files=false) check must be cached on `conformist check` — run once,
// then skipped while none of its matched files change, re-run when a matched file
// changes, and forced to run under --no-cache and -c (clear cache).
//
// This is a test-first specification and currently FAILS: runCheck passes a nil
// cache db ("read-only"), so whole-tree checks run on every invocation. A small
// single-batch tree keeps the check to one invocation per run.
func TestCheckWholeTreeCache(tt *testing.T) {
	t := &test_ui.T{T: tt}
	tempDir := t.TempDir()
	test.ChangeWorkDir(t, tempDir)

	aux := t.TempDir()
	marker := filepath.Join(aux, "runs.log")
	script := wholeTreeStub(t, aux, marker)

	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "a.go"), []byte("package a\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "b.go"), []byte("package b\n"), 0o644))

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"whole": {
				Command:     script,
				Includes:    []string{"*.go"},
				PassesFiles: &passesFiles,
			},
		},
	}
	test.WriteConfig(t, configPath, cfg)

	// 1. first check runs the whole-tree linter.
	conformist(t, withArgs("check"), withNoError(t))
	require.Equal(t, 1, markerRuns(t, marker), "first check should run the whole-tree linter")

	// 2. nothing changed -> skipped (cached).
	conformist(t, withArgs("check"), withNoError(t))
	require.Equal(t, 1, markerRuns(t, marker), "unchanged re-check should skip the whole-tree linter")

	// 3. a matched file changed -> re-run.
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "a.go"), []byte("package a // edit\n"), 0o644))
	conformist(t, withArgs("check"), withNoError(t))
	require.Equal(t, 2, markerRuns(t, marker), "a changed matched file should re-run the whole-tree linter")

	// 4. nothing changed again -> skipped.
	conformist(t, withArgs("check"), withNoError(t))
	require.Equal(t, 2, markerRuns(t, marker), "second unchanged re-check should skip again")

	// 5. --no-cache always runs.
	conformist(t, withArgs("check", "--no-cache"), withNoError(t))
	require.Equal(t, 3, markerRuns(t, marker), "--no-cache should always run the whole-tree linter")

	// 6. -c (clear cache) re-runs; the next unchanged check is cached again.
	conformist(t, withArgs("check", "-c"), withNoError(t))
	require.Equal(t, 4, markerRuns(t, marker), "clearing the cache should re-run the whole-tree linter")
	conformist(t, withArgs("check"), withNoError(t))
	require.Equal(t, 4, markerRuns(t, marker), "after clear+run, an unchanged check should be cached again")
}

// TestCheckWholeTreeCacheConfigInvalidation pins amarbel-llc/conformist#16: a
// change to the check's config (here, its includes) must invalidate the cached
// whole-tree entry so the check re-runs even though no file changed.
//
// Test-first; currently FAILS for the same reason (no whole-tree cache exists).
func TestCheckWholeTreeCacheConfigInvalidation(tt *testing.T) {
	t := &test_ui.T{T: tt}
	tempDir := t.TempDir()
	test.ChangeWorkDir(t, tempDir)

	aux := t.TempDir()
	marker := filepath.Join(aux, "runs.log")
	script := wholeTreeStub(t, aux, marker)

	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "a.go"), []byte("package a\n"), 0o644))

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"whole": {
				Command:     script,
				Includes:    []string{"*.go"},
				PassesFiles: &passesFiles,
			},
		},
	}
	test.WriteConfig(t, configPath, cfg)

	// run + cached re-run.
	conformist(t, withArgs("check"), withNoError(t))
	require.Equal(t, 1, markerRuns(t, marker), "first check should run")
	conformist(t, withArgs("check"), withNoError(t))
	require.Equal(t, 1, markerRuns(t, marker), "unchanged re-check should be cached")

	// change the check's includes -> cache entry must invalidate -> re-run.
	cfg.LinterConfigs["whole"].Includes = []string{"*.go", "*.txt"}
	test.WriteConfig(t, configPath, cfg)
	conformist(t, withArgs("check"), withNoError(t))
	require.Equal(t, 2, markerRuns(t, marker), "a config change should invalidate the whole-tree cache")
}

// TestCheckWholeTreeCacheFindingsNotCached pins amarbel-llc/conformist#16: a
// whole-tree check that reports findings must NOT be cached, so it re-reports on
// the next run (with nothing changed) until the problem is fixed.
func TestCheckWholeTreeCacheFindingsNotCached(tt *testing.T) {
	t := &test_ui.T{T: tt}
	tempDir := t.TempDir()
	test.ChangeWorkDir(t, tempDir)

	aux := t.TempDir()
	marker := filepath.Join(aux, "runs.log")

	// stub whole-tree check: record a run, then ALWAYS report a finding (exit 1).
	script := filepath.Join(aux, "whole.sh")
	body := "#!/usr/bin/env bash\nprintf 'ran\\n' >> '" + marker + "'\nexit 1\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "a.go"), []byte("package a\n"), 0o644))

	configPath := filepath.Join(tempDir, "conformist.toml")
	passesFiles := false
	cfg := &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"whole": {
				Command:     script,
				Includes:    []string{"*.go"},
				PassesFiles: &passesFiles,
			},
		},
	}
	test.WriteConfig(t, configPath, cfg)

	hasFinding := func(as *require.Assertions, err error) { as.Error(err) }

	conformist(t, withArgs("check"), withError(hasFinding))
	require.Equal(t, 1, markerRuns(t, marker), "first failing check should run")

	// nothing changed, but a failing check is never cached -> it must re-run.
	conformist(t, withArgs("check"), withError(hasFinding))
	require.Equal(t, 2, markerRuns(t, marker), "a failing whole-tree check must re-run (not be cached)")
}
