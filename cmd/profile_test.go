package cmd_test

import (
	"fmt"
	"os"
	"os/exec"
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

// bashShebang returns a shebang naming bash by absolute path. The profile tests
// run with an EMPTY PATH, so an artifact script cannot find its interpreter by
// name — and, the point of that bare environment, no jq can be found either.
func bashShebang(t *test_ui.T) string {
	t.Helper()

	bash, err := exec.LookPath("bash")
	require.NoError(t, err)

	return "#!" + bash + "\n"
}

// modelScript is a stand-in for the delivered `just`: it prints a recipe model
// naming the given recipes.
func modelScript(t *test_ui.T, names ...string) string {
	t.Helper()

	recipes := make([]string, 0, len(names))
	for _, n := range names {
		recipes = append(recipes, fmt.Sprintf(`{"name":%q}`, n))
	}

	return bashShebang(t) + "echo '{\"recipes\":[" + strings.Join(recipes, ",") + "]}'\n"
}

// writeProfileTree makes a tree holding a justfile (the linter's trigger) and
// cfg as its conformist.toml, plus — outside the tree, so neither is walked —
// the artifact built from script and a profile pinning it. With tamper, the pin
// is for different bytes. PATH is set to an empty directory for the run. It
// returns the profile's path.
func writeProfileTree(t *test_ui.T, script string, tamper bool, cfg *config.Config) string {
	t.Helper()

	tree := t.TempDir()
	test.ChangeWorkDir(t, tree)

	aux := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(aux, "cache"))
	// A bare environment: nothing is found by name except what the profile
	// delivers, so a passing rule proves its jq is conformist's own.
	t.Setenv("PATH", t.TempDir())

	require.NoError(t, os.WriteFile(filepath.Join(tree, "justfile"), []byte("build:\n"), 0o644))
	test.WriteConfig(t, filepath.Join(tree, "conformist.toml"), cfg)

	artifact := filepath.Join(aux, "fakejust-src")
	require.NoError(t, os.WriteFile(artifact, []byte(script), 0o644))

	pinned := []byte(script)
	if tamper {
		pinned = []byte(script + "# tampered\n")
	}

	profilePath := filepath.Join(aux, "test.profile")
	doc := fmt.Sprintf(profileTemplate, "file://"+artifact, profile.NewMarklID("blake2b256", pinned).String())
	require.NoError(t, os.WriteFile(profilePath, []byte(doc), 0o644))

	return profilePath
}

// TestCheckProfile drives `check --profile` (RFC 0005 POC v1) end to end, in an
// environment whose PATH is empty: the artifact is fetched, verified, reached by
// bare name on PATH, and its output judged by an inline rule run by conformist's
// embedded jq, never an ambient one, and never through a shell command line.
func TestCheckProfile(tt *testing.T) {
	hasFindings := func(as *require.Assertions, err error) { as.ErrorIs(err, cmd.ErrCheckFindings) }

	outputContains := func(t *test_ui.T, want string) func([]byte) {
		return func(out []byte) { require.Contains(t, string(out), want) }
	}

	// Positive control for the bare environment: a linter that calls `jq` by name
	// must NOT find one. Without this, the passes below could be using an ambient
	// jq the empty PATH failed to hide.
	tt.Run("no ambient jq is reachable", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		passesFiles := false
		cfg := &config.Config{LinterConfigs: map[string]*config.Linter{
			"ambient-jq": {Command: "jq -n 1 >/dev/null", Includes: []string{"justfile"}, PassesFiles: &passesFiles},
		}}
		p := writeProfileTree(t, modelScript(t, "build"), false, cfg)

		conformist(t, withArgs("check", "--profile", p),
			withError(hasFindings), withStdout(outputContains(t, "lint findings (ambient-jq)")))
	})

	tt.Run("clean", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript(t, "build", "build-go"), false, &config.Config{})

		// Passing requires `fakejust` to resolve by bare name and the rule's jq
		// to run with no jq on PATH: either failing fails the pipeline, which is
		// itself reported as a finding.
		conformist(t, withArgs("check", "--profile", p), withNoError(t))
	})

	tt.Run("finding", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript(t, "build", "oops"), false, &config.Config{})

		conformist(t, withArgs("check", "--profile", p),
			withError(hasFindings), withStderr(outputContains(t, "'oops' is not a build recipe")))
	})

	// The vacuous-pass guard: a producer that fails must not feed the rule empty
	// input and pass.
	tt.Run("failing producer is not a pass", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		script := bashShebang(t) + "echo 'cannot read the justfile' >&2\nexit 1\n"
		p := writeProfileTree(t, script, false, &config.Config{})

		conformist(t, withArgs("check", "--profile", p),
			withError(hasFindings), withStderr(outputContains(t, "profile rule pipeline failed")))
	})

	tt.Run("tampered artifact is an operational error", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript(t, "build"), true, &config.Config{})

		conformist(t, withArgs("check", "--profile", p), withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, cmd.ErrCheckOperational)
			as.ErrorIs(err, profile.ErrMarklMismatch)
		}))
	})

	// A profile run beside a config shares one exit code; the per-source lines
	// must say which side failed. Here only the config's linter does.
	failingConfig := func() *config.Config {
		passesFiles := false

		return &config.Config{LinterConfigs: map[string]*config.Linter{
			"config-only": {Command: "exit 1", Includes: []string{"justfile"}, PassesFiles: &passesFiles},
		}}
	}

	tt.Run("verdict is reported per source", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript(t, "build"), false, failingConfig())

		conformist(t, withArgs("check", "--profile", p), withError(hasFindings), withStdout(func(out []byte) {
			require.Contains(t, string(out), "profile "+p+": clean")
			require.Contains(t, string(out), "config: findings from config-only")
		}))
	})

	tt.Run("verdict names the profile when the profile fails", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript(t, "oops"), false, &config.Config{})

		conformist(t, withArgs("check", "--profile", p), withError(hasFindings), withStdout(func(out []byte) {
			require.Contains(t, string(out), "profile "+p+": findings from recipe-names")
			require.Contains(t, string(out), "config: clean")
		}))
	})

	tt.Run("--profile-only skips the config's tools", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript(t, "build"), false, failingConfig())

		conformist(t, withArgs("check", "--profile", p, "--profile-only"), withNoError(t), withStdout(func(out []byte) {
			require.Contains(t, string(out), "profile "+p+": clean")
			require.NotContains(t, string(out), "config:", "a profile-only run has no config verdict")
		}))
	})

	tt.Run("--profile-only still fails on the profile's own findings", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript(t, "oops"), false, &config.Config{})

		conformist(t, withArgs("check", "--profile", p, "--profile-only"), withError(hasFindings))
	})

	tt.Run("--profile-only needs no config file", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript(t, "build"), false, &config.Config{})
		require.NoError(t, os.Remove("conformist.toml"))

		conformist(t, withArgs("check", "--profile", p, "--profile-only"), withNoError(t))
	})

	// Pinning a key turns signature verification on for a local profile: an
	// unsigned file must then be refused, not run.
	tt.Run("--profile-key refuses an unsigned local profile", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		p := writeProfileTree(t, modelScript(t, "build"), false, &config.Config{})
		// papi's §15 vector TEST key: well-formed, so the refusal below is for the
		// missing signature and not a malformed pin.
		key := "piggy-piv_auth-v1@ssh_ecdsa_nistp256_pub-q0xr4jwmxnwsf9hkp923ay99rg0c3gaxaepj0ua0f6sds3pxdtc0uxh26y4"

		conformist(t, withArgs("check", "--profile", p, "--profile-key", key),
			withError(func(as *require.Assertions, err error) {
				as.ErrorIs(err, cmd.ErrCheckOperational)
				as.ErrorIs(err, profile.ErrUnsigned)
			}))
	})

	// A plain-http profile URL is refused as a URL, not misread as a local path.
	tt.Run("an http profile URL is refused", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		writeProfileTree(t, modelScript(t, "build"), false, &config.Config{})
		key := "piggy-piv_auth-v1@ssh_ecdsa_nistp256_pub-q0xr4jwmxnwsf9hkp923ay99rg0c3gaxaepj0ua0f6sds3pxdtc0uxh26y4"

		conformist(t, withArgs("check", "--profile", "http://unused.invalid/papi/conformist-profile", "--profile-key", key),
			withError(func(as *require.Assertions, err error) {
				as.ErrorIs(err, cmd.ErrCheckOperational)
				as.ErrorIs(err, profile.ErrUnsupportedScheme)
			}))
	})

	tt.Run("--profile-only without --profile is an operational error", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		writeProfileTree(t, modelScript(t, "build"), false, &config.Config{})

		conformist(t, withArgs("check", "--profile-only"), withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, cmd.ErrCheckOperational)
		}))
	})

	tt.Run("conformist.toml wins a name clash", func(tt *testing.T) {
		t := &test_ui.T{T: tt}
		passesFiles := false
		cfg := &config.Config{LinterConfigs: map[string]*config.Linter{
			// A shell builtin, since nothing is found by name on the empty PATH.
			"recipe-names": {Command: "exit 0", Includes: []string{"justfile"}, PassesFiles: &passesFiles},
		}}
		p := writeProfileTree(t, modelScript(t, "oops"), false, cfg)

		conformist(t, withArgs("check", "--profile", p), withNoError(t))
	})
}
