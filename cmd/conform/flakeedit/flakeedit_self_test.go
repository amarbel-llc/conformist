package flakeedit_test

import (
	"os"
	"testing"

	"code.linenisgreat.com/conformist/cmd/conform/flakeedit"
	"github.com/stretchr/testify/require"
)

// justUsBeforeConform is the exact flake.nix of just-us at 72e8e363, the commit
// before it was moved onto the fleet shape (6b271b0c) and the repo conformist#130
// was seen on. It carries bespoke conformist wiring: a `conformist` input and a
// hand-rolled `conformistEval = inputs.conformist.lib.evalModule …` binding, with
// `flake-utils` rather than `utils`.
func justUsBeforeConform(t *testing.T) []byte { //testui:allow // fixture loader
	t.Helper()

	src, err := os.ReadFile("testdata/just-us-72e8e363.flake.txt")
	require.NoError(t, err)

	return src
}

// TestApplyLeavesForeignEvalModuleWiringAlone pins conformist#130 part 3: a flake
// that already calls conformist.lib.evalModule under its OWN binding name
// (`conformistEval`) is not conformist's wiring and not a clean slate either.
// Splicing `eval`/`impureEval` beside it produced a second evalModule call, so
// Apply refuses and the caller prints what to wire by hand.
func TestApplyLeavesForeignEvalModuleWiringAlone(t *testing.T) {
	src := justUsBeforeConform(t)

	out, _, err := flakeedit.Apply(src, flakeedit.Options{RepoName: "just-us"})
	require.ErrorIs(t, err, flakeedit.ErrUnrecognized)
	require.Equal(t, string(src), string(out), "a refusal must return the source unchanged")
}

// flakeUtilsNamed names the utils flake `flake-utils` throughout: the input, the
// outputs argument and the eachDefaultSystem head. It has no `utils` anywhere.
const flakeUtilsNamed = `{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
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

// TestApplyDoesNotDuplicateTheUtilsInput pins conformist#130 part 2: conform added
// a second, unused `utils` input beside the flake's own `flake-utils`. The
// recognized shape names its utils flake in the eachDefaultSystem head, whatever
// it is called; that name is what conformist and just-us must follow.
func TestApplyDoesNotDuplicateTheUtilsInput(t *testing.T) {
	out, _, err := flakeedit.Apply([]byte(flakeUtilsNamed), flakeedit.Options{})
	require.NoError(t, err)

	got := string(out)

	require.NotContains(t, got, "utils.follows = \"conformist/utils\"",
		"no second utils input beside the flake's own flake-utils")
	require.NotRegexp(t, `(?m)^\s*(inputs\.)?utils\.`, got)
	require.Contains(t, got, `conformist.inputs.utils.follows = "flake-utils";`)
	require.Contains(t, got, `just-us.inputs.flake-utils.follows = "flake-utils";`)
}

// TestApplyKeepsAddingUtilsWhenTheHeadNamesIt is the control for the test above:
// when the eachDefaultSystem head IS `utils` and no such input exists, conform
// still adds it (#83's guarantee), so the fix did not just stop adding it.
func TestApplyKeepsAddingUtilsWhenTheHeadNamesIt(t *testing.T) {
	const headUtilsNoInput = `{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs, utils }:
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

	out, _, err := flakeedit.Apply([]byte(headUtilsNoInput), flakeedit.Options{})
	require.NoError(t, err)
	require.Contains(t, string(out), `utils.follows = "conformist/utils";`)
}

// TestApplyInRepoJustUsDoesNotImportItself pins conformist#130 part 1: in the
// repo that IS just-us, a `just-us` flake input points the flake at itself. Skip
// it, and render every reference to it through `self`, or the spliced bindings
// would name an input that does not exist.
func TestApplyInRepoJustUsDoesNotImportItself(t *testing.T) {
	out, report, err := flakeedit.Apply([]byte(brownfield), flakeedit.Options{RepoName: "just-us"})
	require.NoError(t, err)
	require.True(t, report.Changed())

	got := string(out)

	require.NotContains(t, got, "just-us.url", "the repo is just-us: no self-referential input")
	require.NotContains(t, got, "just-us.inputs")
	require.NotContains(t, got, "just-us,", "no outputs argument for an input that is not there")
	require.NotContains(t, got, "just-us.packages")
	require.NotContains(t, got, "just-us.lib")
	require.Contains(t, got, "justPkg = self.packages.${system}.default;")
	require.Contains(t, got, "self.lib.conformistPresets.justfile")

	// conformist is a separate repo from just-us: it is still imported as usual.
	require.Contains(t, got, `conformist.url = "git+https://code.linenisgreat.com/conformist.git";`)
	require.Contains(t, got, "conformistPkg = conformist.packages.${system}.default;")

	// Idempotent: a second pass adds nothing.
	twice, report, err := flakeedit.Apply(out, flakeedit.Options{RepoName: "just-us"})
	require.NoError(t, err)
	require.False(t, report.Changed(), "second apply must add nothing")
	require.Equal(t, got, string(twice))
}

// TestApplyInRepoConformistDoesNotImportItself is the same rule for conformist's
// own repo: the conformist input is skipped and the bindings go through `self`.
func TestApplyInRepoConformistDoesNotImportItself(t *testing.T) {
	out, _, err := flakeedit.Apply([]byte(brownfield), flakeedit.Options{RepoName: "conformist"})
	require.NoError(t, err)

	got := string(out)

	require.NotContains(t, got, "conformist.url")
	require.NotContains(t, got, "conformist.inputs")
	require.Contains(t, got, "conformistPkg = self.packages.${system}.default;")
	require.Contains(t, got, "self.lib.evalModule pkgs")
	require.Contains(t, got, "self.lib.presets.eng")
	require.NotContains(t, got, "conformist.lib")
	require.NotContains(t, got, "conformist.packages")

	// just-us is a separate repo from conformist: still imported.
	require.Contains(t, got, `just-us.url = "git+https://code.linenisgreat.com/just-us.git";`)
}

// TestApplyOrdinaryRepoNameChangesNothing is the control: any other repo name
// takes the unchanged path, byte for byte.
func TestApplyOrdinaryRepoNameChangesNothing(t *testing.T) {
	plain, _, err := flakeedit.Apply([]byte(brownfield), flakeedit.Options{})
	require.NoError(t, err)

	named, _, err := flakeedit.Apply([]byte(brownfield), flakeedit.Options{RepoName: "somerepo"})
	require.NoError(t, err)
	require.Equal(t, string(plain), string(named))
}
