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
	assert.Empty(t, report.Satisfied)
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
	assert.Empty(t, report.Satisfied)
	assert.True(t, report.Changed())
	assert.Equal(t, []string{`removed "pkgs.just"`}, report.Applied)

	outStr := string(out)
	assert.NotContains(t, outStr, "pkgs.just")
}

func TestClobber_NotFound(t *testing.T) {
	// When neither Old nor New is present, the migration is N/A for this
	// file: no error, no change, no Satisfied entry.
	migrations := []ListElementMigration{
		{Old: "pkgs.nonexistent", New: "something"},
	}

	out, report, err := Clobber([]byte(minimalFlake), migrations)
	require.NoError(t, err)
	assert.False(t, report.Changed())
	assert.Empty(t, report.Applied)
	assert.Empty(t, report.Satisfied)
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

	// Second pass on already-migrated source: Old absent, New present →
	// elementSatisfied → no-op with Satisfied entry.
	out2, rep2, err := Clobber(out1, migrations)
	require.NoError(t, err)
	assert.False(t, rep2.Changed(), "second pass should be a no-op")
	assert.NotEmpty(t, rep2.Satisfied, "second pass should report satisfied")
	assert.Equal(t, out1, out2, "output should be stable")
}

func TestClobber_PartialState(t *testing.T) {
	// Two migrations: one already satisfied, one still pending.
	// Clobber must refuse and return ErrPartialState without applying
	// any edits.
	src := `{
  inputs = {
    utils.url = "github:numtide/flake-utils";
    conformist.url = "git+https://code.linenisgreat.com/conformist.git";
    just-us.url = "git+https://code.linenisgreat.com/just-us.git";
  };

  outputs = { self, utils, conformist, just-us }:
    utils.lib.eachDefaultSystem (system: let
      conformistPkg = conformist.packages.${system}.default;
      justPkg = just-us.packages.${system}.default;
      eval = x: x;
      impureEval = x: x;
    in {
      devShells.default = pkgs.mkShell {
        packages = [
          justPkg
          pkgs.old-thing
        ];
      };
    });
}
`
	migrations := []ListElementMigration{
		{Old: "pkgs.just", New: "justPkg"},     // satisfied: justPkg present
		{Old: "pkgs.old-thing", New: "newPkg"}, // pending: pkgs.old-thing present
	}

	out, _, err := Clobber([]byte(src), migrations)
	require.ErrorIs(t, err, ErrPartialState)
	assert.Equal(t, []byte(src), out, "partial-state refusal must not modify src")
}

func TestClobber_UnrecognizedShape(t *testing.T) {
	// A non-eachDefaultSystem flake is an error, not a silent skip.
	badFlake := `{
  outputs = { self }: {
    formatter = self;
  };
}
`
	out, _, err := Clobber([]byte(badFlake), []ListElementMigration{{Old: "x", New: "y"}})
	require.ErrorIs(t, err, ErrUnrecognized)
	assert.Equal(t, []byte(badFlake), out, "unrecognized flake must be returned unchanged")
}

func TestClobber_NoDevShell(t *testing.T) {
	// Recognized shape with no devShells.default packages list is an error.
	out, _, err := Clobber(
		[]byte(noDevShellFlake),
		[]ListElementMigration{{Old: "pkgs.just", New: "justPkg"}},
	)
	require.ErrorIs(t, err, ErrNoDevShell)
	assert.Equal(t, []byte(noDevShellFlake), out, "no-devshell flake must be returned unchanged")
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
