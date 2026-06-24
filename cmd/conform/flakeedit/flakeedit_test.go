package flakeedit_test

import (
	"strings"
	"testing"

	"github.com/amarbel-llc/conformist/cmd/conform/flakeedit"
	"github.com/stretchr/testify/require"
)

// brownfield is a minimal recognized-shape flake that does not yet
// reference conformist. It already has its own nixpkgs/utils inputs and a
// formatter, exercising input-skip and the formatter conflict path.
const brownfield = `{
  description = "demo";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages.default = pkgs.hello;
        formatter = pkgs.nixfmt-rfc-style;
      }
    );
}
`

// brownfieldEdited is the exact expected output of Apply(brownfield); a
// byte-for-byte golden guards the splice arithmetic against regressions.
// Verified parseable with `nix-instantiate --parse`.
const brownfieldEdited = `{
  description = "demo";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
    conformist.url = "github:amarbel-llc/conformist";
  };

  outputs =
    {
      conformist,
      self,
      nixpkgs,
      utils,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };

        conformistPkg = conformist.packages.${system}.default;

        eval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng
            ./conformist.nix
          ];
          package = conformistPkg;
        };

        impureEval = conformist.lib.evalModule pkgs {
          imports = [ conformist.lib.presets.eng-impure ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
        };
      in
      {
        packages.default = pkgs.hello;
        formatter = pkgs.nixfmt-rfc-style;

        checks.formatting = eval.config.build.check self;
        packages.conformist-impure-config = impureEval.config.build.configFile;
        packages.conformist-pre-commit = eval.config.build.preCommit;
        packages.conformist-repair = eval.config.build.repair;
        devShells.default = pkgs.mkShell {
          packages = [
            conformistPkg
            eval.config.build.preCommit
            eval.config.build.repair
            pkgs.just
          ];
        };
      }
    );
}
`

func TestApplyBrownfieldGolden(t *testing.T) {
	out, _, err := flakeedit.Apply([]byte(brownfield))
	require.NoError(t, err)
	require.Equal(t, brownfieldEdited, string(out))
}

func TestApplyBrownfield(t *testing.T) {
	out, report, err := flakeedit.Apply([]byte(brownfield))
	require.NoError(t, err)
	require.True(t, report.Changed())

	got := string(out)

	// conformist input added; the repo's own nixpkgs/utils left as urls.
	require.Contains(t, got, `conformist.url = "github:amarbel-llc/conformist";`)
	require.NotContains(t, got, "nixpkgs.follows", "must not add follows for a pre-existing nixpkgs input")
	require.NotContains(t, got, "utils.follows", "must not add follows for a pre-existing utils input")

	// outputs argument, let bindings, and the non-conflicting outputs.
	require.Contains(t, got, "conformist,")
	require.Contains(t, got, "conformistPkg = conformist.packages.${system}.default;")
	require.Contains(t, got, "eval = conformist.lib.evalModule pkgs {")
	require.Contains(t, got, "impureEval = conformist.lib.evalModule pkgs {")
	require.Contains(t, got, "checks.formatting = eval.config.build.check self;")
	require.Contains(t, got, "packages.conformist-pre-commit = eval.config.build.preCommit;")
	require.Contains(t, got, "devShells.default = pkgs.mkShell {")

	// the existing formatter is a conflict: left untouched, not overwritten.
	require.Contains(t, report.Conflicts, "formatter")
	require.Contains(t, got, "formatter = pkgs.nixfmt-rfc-style;")
	require.NotContains(t, got, "formatter = eval.config.build.wrapper;")

	// the repo's own output is preserved.
	require.Contains(t, got, "packages.default = pkgs.hello;")
}

// TestApplyIdempotent verifies a second Apply over the output of the
// first is a no-op: the wired flake re-parses and nothing is re-added.
func TestApplyIdempotent(t *testing.T) {
	once, _, err := flakeedit.Apply([]byte(brownfield))
	require.NoError(t, err)

	twice, report, err := flakeedit.Apply(once)
	require.NoError(t, err)
	require.False(t, report.Changed(), "second apply must add nothing")
	require.Empty(t, report.Conflicts, "an already-wired flake reports no conflicts")
	require.Equal(t, string(once), string(twice), "second apply must be byte-identical")
}

// TestApplyUnrecognized verifies non-eachDefaultSystem shapes fall back
// (ErrUnrecognized, source unchanged).
func TestApplyUnrecognized(t *testing.T) {
	cases := map[string]string{
		"not a flake":  "# just a comment\n",
		"raw genAttrs": "{\n  outputs = { self }: { packages.x86_64-linux.default = 1; };\n}\n",
		"no let in":    "{\n  outputs = { self, utils }: utils.lib.eachDefaultSystem (system: { x = 1; });\n}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, err := flakeedit.Apply([]byte(src))
			require.ErrorIs(t, err, flakeedit.ErrUnrecognized)
			require.Equal(t, src, string(out), "source must be returned unchanged on fallback")
		})
	}
}

func TestApplyNoFollowsWhenInputsFresh(t *testing.T) {
	// A flake whose inputs block has none of conformist/nixpkgs/utils:
	// all three conformist inputs are added.
	const fresh = `{
  inputs = {
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { self, utils }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages.default = pkgs.hello;
      }
    );
}
`
	out, _, err := flakeedit.Apply([]byte(fresh))
	require.NoError(t, err)
	got := string(out)
	require.Contains(t, got, `conformist.url = "github:amarbel-llc/conformist";`)
	require.Contains(t, got, `nixpkgs.follows = "conformist/nixpkgs-master";`)
	require.Contains(t, got, `utils.follows = "conformist/utils";`)
	// idempotent inner: the new inputs land inside the existing block.
	require.Equal(t, 1, strings.Count(got, "conformist.url"))
}
