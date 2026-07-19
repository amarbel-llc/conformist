package config_test

import (
	"testing"

	"code.linenisgreat.com/conformist/config"
	"github.com/stretchr/testify/require"
)

func TestConfigIdentity(t *testing.T) {
	as := require.New(t)

	base := func() *config.Config {
		return &config.Config{
			Walk:     "auto",
			Excludes: []string{"*.md", "flake.lock"},
			FormatterConfigs: map[string]*config.Formatter{
				"gofumpt": {
					Command:  "/nix/store/aaaaaaaa-gofumpt-0.10.0/bin/gofumpt",
					Includes: []string{"*.go"},
					Priority: 2,
				},
				"goimports": {
					Command:  "/nix/store/bbbbbbbb-gotools/bin/goimports",
					Includes: []string{"*.go"},
					Priority: 1,
				},
			},
		}
	}

	id := base().Identity()
	as.Regexp("^[0-9a-f]{64}$", id, "identity should be a hex sha256")

	// Deterministic: recomputing over an equal config (with the map rebuilt, so
	// Go's randomized iteration order is exercised) yields the same identity.
	as.Equal(id, base().Identity())

	// A rotated toolchain store path (a version bump) changes the identity.
	bumped := base()
	bumped.FormatterConfigs["gofumpt"].Command = "/nix/store/cccccccc-gofumpt-0.11.0/bin/gofumpt"
	as.NotEqual(id, bumped.Identity(), "a different toolchain store path must change the identity")

	// A different tool set changes the identity.
	withLinter := base()
	withLinter.LinterConfigs = map[string]*config.Linter{
		"shellcheck": {Command: "shellcheck", Includes: []string{"*.sh"}},
	}
	as.NotEqual(id, withLinter.Identity(), "adding a linter must change the identity")

	// Different global excludes change the identity.
	reExcluded := base()
	reExcluded.Excludes = []string{"*.md"}
	as.NotEqual(id, reExcluded.Identity(), "different excludes must change the identity")

	// An empty config still produces a stable hash (no panic on nil maps).
	empty := &config.Config{}
	as.Regexp("^[0-9a-f]{64}$", empty.Identity())
	as.Equal(empty.Identity(), (&config.Config{}).Identity())
}
