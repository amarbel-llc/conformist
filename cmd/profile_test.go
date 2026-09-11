package cmd_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/cmd"
	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/profile"
	"code.linenisgreat.com/conformist/test"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// profileTemplate is a profile delivering a "fakejust" artifact and one rule
// stanza over its output; %q slots take the artifact url and its pin.
const profileTemplate = `---
! toml-conformist_profile-v1
---

[artifact.fakejust]
form = "static"
url = %q
markl = %q

[linter.recipe-names]
command = "fakejust --dump"
rule-tool = "jq"
includes = ["justfile"]
passes-files = false
rule = '''
.recipes[] | select(.name | startswith("build") | not) | "'\(.name)' is not a build recipe"
'''
`

// modelScript is a stand-in for the delivered `just`: it prints a recipe model
// naming the given recipes.
func modelScript(names ...string) string {
	recipes := make([]string, 0, len(names))
	for _, n := range names {
		recipes = append(recipes, fmt.Sprintf(`{"name":%q}`, n))
	}

	return "#!/usr/bin/env bash\necho '{\"recipes\":[" + strings.Join(recipes, ",") + "]}'\n"
}

// writeProfileTree makes a tree holding a justfile (the linter's trigger) and
// cfg as its conformist.toml, plus — outside the tree, so neither is walked —
// the artifact built from script and a profile pinning it. With tamper, the pin
// is for different bytes. It returns the profile's path.
func writeProfileTree(t *test_ui.T, script string, tamper bool, cfg *config.Config) string {
	t.Helper()

	tree := t.TempDir()
	test.ChangeWorkDir(t, tree)

	aux := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(aux, "cache"))
	// The run prepends artifact dirs to PATH in-process; Setenv restores it.
	t.Setenv("PATH", os.Getenv("PATH"))

	require.NoError(t, os.WriteFile(filepath.Join(tree, "justfile"), []byte("build:\n"), 0o644))
	test.WriteConfig(t, filepath.Join(tree, "conformist.toml"), cfg)

	artifact := filepath.Join(aux, "fakejust-src")
	require.NoError(t, os.WriteFile(artifact, []byte(script), 0o644))

	pinned := []byte(script)
	if tamper {
		pinned = []byte(script + "# tampered\n")
	}

	profilePath := filepath.Join(aux, "test.profile")
	doc := fmt.Sprintf(profileTemplate, "file://"+artifact, profile.NewSHA256MarklID(pinned).String())
	require.NoError(t, os.WriteFile(profilePath, []byte(doc), 0o644))

	return profilePath
}

// TestCheckProfile drives `check --profile` (RFC 0005 POC v1) end to end: the
// artifact is fetched, verified, reached by bare name on PATH, and its output
// judged by an inline rule that never touches a shell command line.
func TestCheckProfile(tt *testing.T) {
	hasFindings := func(as *require.Assertions, err error) { as.ErrorIs(err, cmd.ErrCheckFindings) }

	stderrContains := func(t *test_ui.T, want string) func([]byte) {
		return func(out []byte) { require.Contains(t, string(out), want) }
	}

	tt.Run("clean", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript("build", "build-go"), false, &config.Config{})

		// Passing requires `fakejust` to resolve by bare name: a command-not-found
		// would fail the pipeline, which is itself reported as a finding.
		conformist(t, withArgs("check", "--profile", p), withNoError(t))
	})

	tt.Run("finding", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript("build", "oops"), false, &config.Config{})

		conformist(t, withArgs("check", "--profile", p),
			withError(hasFindings), withStderr(stderrContains(t, "'oops' is not a build recipe")))
	})

	// The vacuous-pass guard: a producer that fails must not feed the rule empty
	// input and pass.
	tt.Run("failing producer is not a pass", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		script := "#!/usr/bin/env bash\necho 'cannot read the justfile' >&2\nexit 1\n"
		p := writeProfileTree(t, script, false, &config.Config{})

		conformist(t, withArgs("check", "--profile", p),
			withError(hasFindings), withStderr(stderrContains(t, "profile rule pipeline failed")))
	})

	tt.Run("tampered artifact is an operational error", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript("build"), true, &config.Config{})

		conformist(t, withArgs("check", "--profile", p), withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, cmd.ErrCheckOperational)
			as.ErrorIs(err, profile.ErrMarklMismatch)
		}))
	})

	tt.Run("conformist.toml wins a name clash", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		passesFiles := false
		cfg := &config.Config{LinterConfigs: map[string]*config.Linter{
			"recipe-names": {Command: "true", Includes: []string{"justfile"}, PassesFiles: &passesFiles},
		}}
		p := writeProfileTree(t, modelScript("oops"), false, cfg)

		conformist(t, withArgs("check", "--profile", p), withNoError(t))
	})
}
