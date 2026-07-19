package cmd_test

import (
	"path/filepath"
	"strings"
	"testing"

	formatCmd "code.linenisgreat.com/conformist/cmd/format"
	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/test"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// TestIdentity covers the `conformist identity` subcommand (conformist#76): it
// prints a stable hex identity for the resolved config.
func TestIdentity(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	test.WriteConfig(t, configPath, &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"foo-fmt": {Command: "foo-fmt", Includes: []string{"*.foo"}},
		},
	})

	var first string

	conformist(t, withArgs("identity"), withNoError(t), withStdout(func(out []byte) {
		line := strings.TrimSpace(string(out))
		as.Regexp("^[0-9a-f]{64}$", line, "identity should print a hex sha256")
		first = line
	}))

	// The same config prints the same identity.
	conformist(t, withArgs("identity"), withNoError(t), withStdout(func(out []byte) {
		as.Equal(first, strings.TrimSpace(string(out)))
	}))
}

// TestIdentityMismatch covers the attestation detection (conformist#76): a first
// run records the tree's config identity; a later run under a DIFFERENT config is
// refused with --refuse-identity-mismatch, and merely warns (proceeds) by
// default.
func TestIdentityMismatch(tt *testing.T) {
	t := &test_ui.T{T: tt}

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	// Config A. foo-fmt's binary is absent, but repair/format mode degrades
	// (conformist#75), so the run succeeds and records identity A against the tree.
	test.WriteConfig(t, configPath, &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"foo-fmt": {Command: "foo-fmt", Includes: []string{"*.foo"}},
		},
	})

	conformist(t, withNoError(t))

	// Config B: a different tool set, hence a different identity.
	test.WriteConfig(t, configPath, &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"bar-fmt": {Command: "bar-fmt", Includes: []string{"*.bar"}},
		},
	})

	// Under --refuse-identity-mismatch the divergent config is refused.
	conformist(t, withArgs("--refuse-identity-mismatch"), withError(func(as *require.Assertions, err error) {
		as.ErrorIs(err, formatCmd.ErrIdentityMismatch)
	}))

	// By default the mismatch only warns; the run proceeds (and reconciles the
	// recorded identity to B).
	conformist(t, withNoError(t))
}
