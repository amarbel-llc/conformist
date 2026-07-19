package flakeedit_test

import (
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/cmd/conform/flakeedit"
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
    conformist.url = "git+https://code.linenisgreat.com/conformist.git";
    conformist.inputs.utils.follows = "utils";
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
	out, _, err := flakeedit.Apply([]byte(brownfield), flakeedit.Options{})
	require.NoError(t, err)
	require.Equal(t, brownfieldEdited, string(out))
}

func TestApplyBrownfield(t *testing.T) {
	out, report, err := flakeedit.Apply([]byte(brownfield), flakeedit.Options{})
	require.NoError(t, err)
	require.True(t, report.Changed())

	got := string(out)

	// conformist input added; the repo's own nixpkgs/utils left as urls. The
	// shared utils input is deduped from INSIDE the conformist input; no
	// top-level input is ever added for a pre-existing name (#83).
	require.Contains(t, got, `conformist.url = "git+https://code.linenisgreat.com/conformist.git";`)
	require.Contains(t, got, `conformist.inputs.utils.follows = "utils";`)
	require.NotContains(t, got, "nixpkgs.follows", "must not add follows for a pre-existing nixpkgs input")
	require.NotContains(t, got, `"conformist/utils"`, "must not add a top-level follows for a pre-existing utils input")

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
	once, _, err := flakeedit.Apply([]byte(brownfield), flakeedit.Options{})
	require.NoError(t, err)

	twice, report, err := flakeedit.Apply(once, flakeedit.Options{})
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
			out, _, err := flakeedit.Apply([]byte(src), flakeedit.Options{})
			require.ErrorIs(t, err, flakeedit.ErrUnrecognized)
			require.Equal(t, src, string(out), "source must be returned unchanged on fallback")
		})
	}
}

// TestApplyForceFormatter pins #63's --force-formatter: the existing formatter
// value is replaced with conformist's wrapper instead of being a conflict.
func TestApplyForceFormatter(t *testing.T) {
	out, report, err := flakeedit.Apply([]byte(brownfield), flakeedit.Options{ForceFormatter: true})
	require.NoError(t, err)

	got := string(out)

	require.NotContains(t, report.Conflicts, "formatter", "formatter must be replaced, not a conflict")
	require.Contains(t, report.Added, "formatter (replaced)")
	require.Contains(t, got, "formatter = eval.config.build.wrapper;")
	require.NotContains(t, got, "pkgs.nixfmt-rfc-style", "the old formatter value must be gone")
	require.Equal(t, 1, strings.Count(got, "formatter ="), "must not add a second formatter binding")
}

// TestApplyForceFormatterIdempotent verifies re-running with --force-formatter
// over an already-wired flake replaces nothing further.
func TestApplyForceFormatterIdempotent(t *testing.T) {
	once, _, err := flakeedit.Apply([]byte(brownfield), flakeedit.Options{ForceFormatter: true})
	require.NoError(t, err)

	twice, report, err := flakeedit.Apply(once, flakeedit.Options{ForceFormatter: true})
	require.NoError(t, err)
	require.False(t, report.Changed(), "second force-formatter apply must change nothing")
	require.Equal(t, string(once), string(twice))
}

// devShellFlake is a brownfield flake whose devShells.default already declares
// a packages list — the merge target for #63's devShell list-merge.
const devShellFlake = `{
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
        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.go
          ];
        };
      }
    );
}
`

// TestApplyMergesDevShellPackages pins #63's devShell list-merge: when
// devShells.default already exists with a packages list, conform splices its
// tools into that list instead of reporting a conflict.
func TestApplyMergesDevShellPackages(t *testing.T) {
	out, report, err := flakeedit.Apply([]byte(devShellFlake), flakeedit.Options{})
	require.NoError(t, err)
	require.True(t, report.Changed())

	got := string(out)

	// devShells.default is merged, not a conflict.
	require.NotContains(t, report.Conflicts, "devShells.default")
	require.Contains(t, report.Added, "devShells.default packages")

	// the existing item is preserved and conformist's tools are added to the
	// SAME list (no second devShells.default binding).
	require.Contains(t, got, "pkgs.go")
	require.Contains(t, got, "conformistPkg")
	require.Contains(t, got, "eval.config.build.preCommit")
	require.Contains(t, got, "eval.config.build.repair")
	require.Equal(t, 1, strings.Count(got, "devShells.default ="),
		"must merge into the existing devShells.default, not add a second one")
	require.NotContains(t, got, "devShells.default = pkgs.mkShell {\n          packages = [\n            conformistPkg",
		"sanity: the original pkgs.go must remain the first item")
}

// TestApplyDevShellMergeIdempotent verifies a second Apply over a merged flake
// adds nothing to the packages list.
func TestApplyDevShellMergeIdempotent(t *testing.T) {
	once, _, err := flakeedit.Apply([]byte(devShellFlake), flakeedit.Options{})
	require.NoError(t, err)

	twice, report, err := flakeedit.Apply(once, flakeedit.Options{})
	require.NoError(t, err)
	require.False(t, report.Changed(), "second apply must add nothing")
	require.Equal(t, string(once), string(twice))
}

// nestedConformist is a flake hand-wired to conformist using NESTED attrsets
// (`checks = { formatting = …; }`, `packages = { conformist-* = …; }`,
// `devShells = { default = …; }`) instead of the dotted form conform writes.
// Apply must recognize these as already present (#63) — keying idempotency on
// dotted paths alone would re-add them and double-define the attrsets.
const nestedConformist = `{
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
          imports = [ conformist.lib.presets.eng ./conformist.nix ];
          package = conformistPkg;
        };
        impureEval = conformist.lib.evalModule pkgs {
          imports = [ conformist.lib.presets.eng-impure ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
        };
      in
      {
        formatter = eval.config.build.wrapper;
        checks = {
          formatting = eval.config.build.check self;
        };
        packages = {
          default = pkgs.hello;
          conformist-impure-config = impureEval.config.build.configFile;
          conformist-pre-commit = eval.config.build.preCommit;
          conformist-repair = eval.config.build.repair;
        };
        devShells = {
          default = pkgs.mkShell {
            packages = [
              conformistPkg
              eval.config.build.preCommit
              eval.config.build.repair
              pkgs.just
            ];
          };
        };
      }
    );
}
`

// TestApplyDetectsNestedConformistAttrs pins #63's nested-attrset idempotency: a
// flake already wired in nested-attrset form is a no-op, not a duplicate-key
// rewrite.
func TestApplyDetectsNestedConformistAttrs(t *testing.T) {
	out, report, err := flakeedit.Apply([]byte(nestedConformist), flakeedit.Options{})
	require.NoError(t, err)
	require.False(t, report.Changed(), "a nested-form wired flake must be a no-op, got added=%v", report.Added)
	require.Equal(t, nestedConformist, string(out))
}

// TestApplyNestedParentConflict verifies conform does not double-define a
// nested attrset: a fresh flake whose `packages` is a nested attrset gets the
// conformist packages.* reported as conflicts rather than spliced in as a
// second dotted `packages` binding (which would be invalid Nix).
func TestApplyNestedParentConflict(t *testing.T) {
	const nestedPackages = `{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { self, nixpkgs, utils }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages = {
          default = pkgs.hello;
        };
      }
    );
}
`
	out, report, err := flakeedit.Apply([]byte(nestedPackages), flakeedit.Options{})
	require.NoError(t, err)

	got := string(out)
	// the conformist packages.* are reported as conflicts, not spliced in as a
	// second dotted `packages` binding (which would double-define packages).
	require.Contains(t, report.Conflicts, "packages.conformist-impure-config")
	require.Contains(t, report.Conflicts, "packages.conformist-pre-commit")
	require.Contains(t, report.Conflicts, "packages.conformist-repair")
	require.NotContains(t, got, "packages.conformist-pre-commit =",
		"must not splice a dotted packages.* alongside the nested packages attrset")
	require.Contains(t, got, "packages = {", "the repo's nested packages attrset is untouched")
}

// outerLet is a minimal flake whose outputs function has an outer `let … in`
// block before eachDefaultSystem (the pattern that previously caused
// ErrUnrecognized — conformist#81). It is not yet wired to conformist.
const outerLet = `{
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
    let
      version = "1.0.0";
    in
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

// outerLetEdited is the expected output of Apply(outerLet): all four splice
// targets are populated and the outer let block is preserved verbatim.
// Verified parseable with `nix-instantiate --parse`.
const outerLetEdited = `{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
    conformist.url = "git+https://code.linenisgreat.com/conformist.git";
    conformist.inputs.utils.follows = "utils";
  };

  outputs =
    {
      conformist,
      self,
      nixpkgs,
      utils,
    }:
    let
      version = "1.0.0";
    in
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

        formatter = eval.config.build.wrapper;
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

// TestApplyOuterLetGolden pins the exact byte output for the outer-let shape
// (#81): the outer let block is preserved and all four splice targets land.
func TestApplyOuterLetGolden(t *testing.T) {
	out, _, err := flakeedit.Apply([]byte(outerLet), flakeedit.Options{})
	require.NoError(t, err)
	require.Equal(t, outerLetEdited, string(out))
}

// TestApplyOuterLet pins the behavioral contract for the outer-let shape
// (conformist#81): the shape is recognized, all four splice targets are
// populated, the outer let block is untouched, and no input follows are
// added for pre-existing nixpkgs/utils.
func TestApplyOuterLet(t *testing.T) {
	out, report, err := flakeedit.Apply([]byte(outerLet), flakeedit.Options{})
	require.NoError(t, err)
	require.True(t, report.Changed())
	require.Empty(t, report.Conflicts)

	got := string(out)

	// outer let block is preserved verbatim.
	require.Contains(t, got, `let
      version = "1.0.0";
    in`)

	// input wired; the pre-existing nixpkgs gets no follows, the shared utils
	// is deduped from inside the conformist input (#83).
	require.Contains(t, got, `conformist.url = "git+https://code.linenisgreat.com/conformist.git";`)
	require.Contains(t, got, `conformist.inputs.utils.follows = "utils";`)
	require.NotContains(t, got, "nixpkgs.follows")
	require.NotContains(t, got, `"conformist/utils"`)

	// outputs arg, let bindings, and return attrs spliced into the inner scope.
	require.Contains(t, got, "conformist,")
	require.Contains(t, got, "conformistPkg = conformist.packages.${system}.default;")
	require.Contains(t, got, "eval = conformist.lib.evalModule pkgs {")
	require.Contains(t, got, "impureEval = conformist.lib.evalModule pkgs {")
	require.Contains(t, got, "formatter = eval.config.build.wrapper;")
	require.Contains(t, got, "checks.formatting = eval.config.build.check self;")
	require.Contains(t, got, "packages.conformist-pre-commit = eval.config.build.preCommit;")
	require.Contains(t, got, "devShells.default = pkgs.mkShell {")

	// the repo's own output is preserved.
	require.Contains(t, got, "packages.default = pkgs.hello;")
}

// TestApplyOuterLetIdempotent verifies a second Apply over the wired
// outer-let flake is a no-op.
func TestApplyOuterLetIdempotent(t *testing.T) {
	once, _, err := flakeedit.Apply([]byte(outerLet), flakeedit.Options{})
	require.NoError(t, err)

	twice, report, err := flakeedit.Apply(once, flakeedit.Options{})
	require.NoError(t, err)
	require.False(t, report.Changed(), "second apply must add nothing")
	require.Empty(t, report.Conflicts)
	require.Equal(t, string(once), string(twice), "second apply must be byte-identical")
}

func TestApplyNoFollowsWhenInputsFresh(t *testing.T) {
	// A flake whose inputs block has none of conformist's own input names:
	// the conformist input is added bare (no follows to wire), plus the
	// top-level utils follows — the ONLY top-level input conform may add,
	// because the recognized shape guarantees the outputs pattern names
	// `utils` (#83). In particular no top-level nixpkgs is added.
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
	out, _, err := flakeedit.Apply([]byte(fresh), flakeedit.Options{})
	require.NoError(t, err)
	got := string(out)
	require.Contains(t, got, `conformist.url = "git+https://code.linenisgreat.com/conformist.git";`)
	require.Contains(t, got, `utils.follows = "conformist/utils";`)
	require.NotContains(t, got, "nixpkgs.follows",
		"must never add a top-level nixpkgs input the outputs pattern does not name (#83)")
	require.NotContains(t, got, "conformist.inputs.",
		"no same-named consumer inputs exist, so no follows are wired inside the conformist input")
	// idempotent inner: the new inputs land inside the existing block.
	require.Equal(t, 1, strings.Count(got, "conformist.url"))
}

// engInputs reproduces the conformist#83 breakage shape: an eng repo with
// igloo + nixpkgs-master + utils inputs, NO nixpkgs, and a STRICT (no-`...`)
// outputs destructuring. The old splice added a top-level
// `nixpkgs.follows = "conformist/nixpkgs-master"`, and the flake call then
// failed eval with "called with unexpected argument 'nixpkgs'" right after a
// "successful" conform (hit on tommy).
const engInputs = `{
  inputs = {
    igloo.url = "github:amarbel-llc/nixpkgs/nixos-unstable";
    nixpkgs-master.url = "github:NixOS/nixpkgs/master";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      utils,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = igloo.legacyPackages.${system};
      in
      {
        packages.default = pkgs.hello;
      }
    );
}
`

// TestApplyEngInputsFollowsInsideConformist pins the #83 fix: with
// igloo/nixpkgs-master-shaped inputs and no nixpkgs, the shared inputs are
// deduped via follows INSIDE the conformist input block, and no new
// top-level input is introduced.
func TestApplyEngInputsFollowsInsideConformist(t *testing.T) {
	out, report, err := flakeedit.Apply([]byte(engInputs), flakeedit.Options{})
	require.NoError(t, err)
	require.True(t, report.Changed())

	got := string(out)

	require.Contains(t, got, `conformist.url = "git+https://code.linenisgreat.com/conformist.git";`)
	require.Contains(t, got, `conformist.inputs.igloo.follows = "igloo";`)
	require.Contains(t, got, `conformist.inputs.nixpkgs-master.follows = "nixpkgs-master";`)
	require.Contains(t, got, `conformist.inputs.utils.follows = "utils";`)

	// the poison pill: no top-level nixpkgs input in any form.
	require.NotContains(t, got, "nixpkgs.url")
	require.NotContains(t, got, "\n    nixpkgs.follows")
	require.NotContains(t, got, "inputs.nixpkgs.follows")
	// and no top-level utils.follows either — the consumer already has utils.
	require.NotContains(t, got, `"conformist/utils"`)
}

// TestApplyEngInputsIdempotent verifies a second Apply over the wired eng
// flake adds nothing.
func TestApplyEngInputsIdempotent(t *testing.T) {
	once, _, err := flakeedit.Apply([]byte(engInputs), flakeedit.Options{})
	require.NoError(t, err)

	twice, report, err := flakeedit.Apply(once, flakeedit.Options{})
	require.NoError(t, err)
	require.False(t, report.Changed(), "second apply must add nothing")
	require.Equal(t, string(once), string(twice))
}
