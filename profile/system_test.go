package profile_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/profile"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

func TestHostSystemIsNixStyle(t *testing.T) {
	require.Regexp(t, `^[a-z0-9_]+-[a-z]+$`, profile.HostSystem())
}

// perSystemProfile writes one build per system (distinct bytes, so a wrong
// selection fails verification) and returns a profile keyed by system.
func perSystemProfile(t *test_ui.T, systems ...string) (string, map[string][]byte) {
	t.Helper()

	dir := t.TempDir()
	builds := map[string][]byte{}

	var src strings.Builder

	src.WriteString("---\n! toml-conformist_profile-v1\n---\n\n[artifact.tool]\nform = \"static\"\n")

	for _, system := range systems {
		content := []byte("#!/bin/sh\n# build for " + system + "\n")
		builds[system] = content

		path := filepath.Join(dir, system)
		require.NoError(t, os.WriteFile(path, content, 0o644))

		fmt.Fprintf(&src, "\n[artifact.tool.system.%s]\nurl = %q\nmarkl = %q\n",
			system, "file://"+path, profile.NewMarklID("sha256", content))
	}

	return src.String(), builds
}

func TestResolveSelectsTheHostSystemsBuild(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	src, builds := perSystemProfile(t, "x86_64-linux", "aarch64-darwin")

	doc, err := profile.Parse("test.profile", []byte(src))
	as.NoError(err)

	for system, want := range builds {
		res, err := profile.Resolver{CacheDir: t.TempDir(), System: system}.Resolve(t.Context(), doc)
		as.NoError(err, system)

		got, err := os.ReadFile(filepath.Join(res.PathDirs[0], "tool"))
		as.NoError(err)
		as.Equal(string(want), string(got), "the %s build must be the one materialized", system)
	}
}

func TestResolveRejectsAnUnsupportedSystem(tt *testing.T) {
	t := &test_ui.T{T: tt}

	src, _ := perSystemProfile(t, "x86_64-linux", "aarch64-darwin")

	doc, err := profile.Parse("test.profile", []byte(src))
	require.NoError(t, err)

	cache := filepath.Join(t.TempDir(), "cache")

	_, err = profile.Resolver{CacheDir: cache, System: "riscv64-linux"}.Resolve(t.Context(), doc)
	require.ErrorIs(t, err, profile.ErrNoArtifactForSystem)
	require.ErrorContains(t, err, "aarch64-darwin, x86_64-linux", "the error must list what IS available")

	_, statErr := os.Stat(cache)
	require.True(t, os.IsNotExist(statErr), "nothing is fetched when the host has no build")
}

func TestParseRejectsBadPerSystemTables(t *testing.T) {
	good, _ := perSystemProfile(&test_ui.T{T: t}, "x86_64-linux")

	for name, tc := range map[string]struct{ old, new string }{
		"both flat and per-system":     {`form = "static"`, "form = \"static\"\nurl = \"file:///x\"\nmarkl = \"x\""},
		"non-nix system key":           {"system.x86_64-linux]", "system.Linux]"},
		"per-system entry lacks markl": {"\nmarkl = ", "\nnotmarkl = "},
	} {
		t.Run(name, func(t *testing.T) {
			src := strings.Replace(good, tc.old, tc.new, 1)
			require.NotEqual(t, good, src, "the case must change the profile")

			_, err := profile.Parse("test.profile", []byte(src))
			require.Error(t, err)
		})
	}
}
