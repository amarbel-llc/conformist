package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalFlake is the smallest eachDefaultSystem flake that ParseFlake
// accepts and that has a devShells.default with a packages list.
const minimalFlake = `{
  inputs = {
    utils.url = "github:numtide/flake-utils";
    conformist.url = "git+https://code.linenisgreat.com/conformist.git";
    just-us.url = "git+https://code.linenisgreat.com/just-us.git";
  };

  outputs = { self, utils, conformist, just-us }:
    utils.lib.eachDefaultSystem (system: let
      pkgs = import <nixpkgs> { inherit system; };
      conformistPkg = conformist.packages.${system}.default;
      justPkg = just-us.packages.${system}.default;
      eval = conformist.lib.evalModule pkgs { package = conformistPkg; };
      impureEval = conformist.lib.evalModule pkgs { package = conformistPkg; };
    in {
      devShells.default = pkgs.mkShell {
        packages = [
          conformistPkg
          eval.config.build.preCommit
          eval.config.build.repair
          pkgs.just
        ];
      };
    });
}
`

// noDevShellFlake has an eachDefaultSystem shape but no devShells.default.
const noDevShellFlake = `{
  inputs = {
    utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, utils }:
    utils.lib.eachDefaultSystem (system: let
      pkgs = import <nixpkgs> { inherit system; };
      conformistPkg = conformist.packages.${system}.default;
      justPkg = just-us.packages.${system}.default;
      eval = x: x;
      impureEval = x: x;
    in {
      formatter = pkgs.nixfmt;
    });
}
`

func TestClobber_ReplaceElement(t *testing.T) {
	migrations := []ListElementMigration{
		{Old: "pkgs.just", New: "justPkg"},
	}

	out, report, err := Clobber([]byte(minimalFlake), migrations)
	require.NoError(t, err)
	assert.Empty(t, report.Skipped)
	assert.True(t, report.Changed())
	assert.Equal(t, []string{`replaced "pkgs.just" with "justPkg"`}, report.Applied)

	outStr := string(out)
	assert.Contains(t, outStr, "justPkg")
	assert.NotContains(t, outStr, "pkgs.just")
}

func TestClobber_DeleteElement(t *testing.T) {
	migrations := []ListElementMigration{
		{Old: "pkgs.just", New: ""},
	}

	out, report, err := Clobber([]byte(minimalFlake), migrations)
	require.NoError(t, err)
	assert.Empty(t, report.Skipped)
	assert.True(t, report.Changed())
	assert.Equal(t, []string{`removed "pkgs.just"`}, report.Applied)

	outStr := string(out)
	assert.NotContains(t, outStr, "pkgs.just")
}

func TestClobber_NotFound(t *testing.T) {
	migrations := []ListElementMigration{
		{Old: "pkgs.nonexistent", New: "something"},
	}

	out, report, err := Clobber([]byte(minimalFlake), migrations)
	require.NoError(t, err)
	assert.Empty(t, report.Skipped)
	assert.False(t, report.Changed())
	assert.Equal(t, []byte(minimalFlake), out, "src should be unchanged")
}

func TestClobber_Idempotent(t *testing.T) {
	migrations := []ListElementMigration{
		{Old: "pkgs.just", New: "justPkg"},
	}

	// First pass.
	out1, rep1, err := Clobber([]byte(minimalFlake), migrations)
	require.NoError(t, err)
	require.True(t, rep1.Changed())

	// Second pass on already-migrated source.
	out2, rep2, err := Clobber(out1, migrations)
	require.NoError(t, err)
	assert.False(t, rep2.Changed(), "second pass should be a no-op")
	assert.Equal(t, out1, out2, "output should be stable")
}

func TestClobber_UnrecognizedShape(t *testing.T) {
	badFlake := `{
  outputs = { self }: {
    formatter = self;
  };
}
`
	out, report, err := Clobber([]byte(badFlake), []ListElementMigration{{Old: "x", New: "y"}})
	require.NoError(t, err)
	assert.NotEmpty(t, report.Skipped)
	assert.Equal(t, []byte(badFlake), out, "unrecognized flake should be returned unchanged")
}

func TestClobber_NoDevShell(t *testing.T) {
	out, report, err := Clobber([]byte(noDevShellFlake), []ListElementMigration{{Old: "pkgs.just", New: "justPkg"}})
	require.NoError(t, err)
	assert.NotEmpty(t, report.Skipped)
	assert.Equal(t, []byte(noDevShellFlake), out, "no-devshell flake should be returned unchanged")
}

func TestTokenIndex(t *testing.T) {
	cases := []struct {
		s      string
		needle string
		want   int
	}{
		{"  pkgs.just\n", "pkgs.just", 2},
		{"  pkgs.justmore\n", "pkgs.just", -1},
		{"  morepkgs.just\n", "pkgs.just", -1},
		{"  pkgs.just pkgs.just\n", "pkgs.just", 2},
		{"justPkg\n", "pkgs.just", -1},
	}
	for _, c := range cases {
		got := tokenIndex(c.s, c.needle)
		assert.Equal(t, c.want, got, "tokenIndex(%q, %q)", c.s, c.needle)
	}
}
