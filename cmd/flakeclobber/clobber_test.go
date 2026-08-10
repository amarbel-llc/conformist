package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	flakeparse "code.linenisgreat.com/conformist/cmd/conform/flakeparse"
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

// postConformFlake is minimalFlake as it looks AFTER `conformist conform` has
// run: conform merged justPkg into the packages list but left pkgs.just in
// place, so both spellings of the same tool are listed (conformist#102).
//
// DERIVED from minimalFlake rather than copied, so the two cannot drift apart.
// A copy would keep passing after minimalFlake's shape changed — these tests
// assert only NotContains "pkgs.just" / Contains "justPkg", which a stale
// fixture still satisfies while no longer exercising the shape it claims to.
var postConformFlake = strings.Replace(
	minimalFlake,
	"          pkgs.just\n",
	"          justPkg\n          pkgs.just\n",
	1,
)

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

// TestClobber_AmbiguousStateNamesTheCompletingCommand pins conformist#102.
// The refusal itself is correct and deliberate — both spellings present is
// genuinely ambiguous — but a refusal that does not name the next step reads
// as a dead end. The operator must be told that the completing operation is a
// DELETE, not a replace, so assert the remediation is actually in the message
// rather than just asserting that some error occurred.
func TestClobber_AmbiguousStateNamesTheCompletingCommand(t *testing.T) {
	// strings.Replace returns the input unchanged when the pattern misses, so
	// without this the fixture could silently degrade to minimalFlake — which
	// has only pkgs.just, is not ambiguous at all, and would fail this test
	// for the wrong reason.
	require.Contains(t, postConformFlake, "          justPkg\n          pkgs.just\n",
		"postConformFlake's derivation from minimalFlake must have applied")

	migrations := []ListElementMigration{
		{Old: "pkgs.just", New: "justPkg"},
	}

	out, report, err := Clobber([]byte(postConformFlake), migrations)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrAmbiguousState)

	msg := err.Error()
	assert.Contains(t, msg, `--old pkgs.just --new ""`,
		"the refusal must name the exact completing command, not just diagnose the state")
	assert.Contains(t, msg, "conformist conform",
		"the refusal should explain how the tree got into this state")

	assert.Equal(t, []byte(postConformFlake), out, "src must be untouched on refusal")
	assert.Empty(t, report.Applied)
	assert.Empty(t, report.Satisfied)
}

// TestClobber_DeleteCompletesAfterConform verifies the command the message
// above recommends actually works on that same input — otherwise the advice
// would be untested prose.
func TestClobber_DeleteCompletesAfterConform(t *testing.T) {
	migrations := []ListElementMigration{
		{Old: "pkgs.just", New: ""},
	}

	out, report, err := Clobber([]byte(postConformFlake), migrations)
	require.NoError(t, err)
	assert.True(t, report.Changed())
	assert.Equal(t, []string{`removed "pkgs.just"`}, report.Applied)

	outStr := string(out)
	assert.NotContains(t, outStr, "pkgs.just")
	assert.Contains(t, outStr, "justPkg", "the surviving spelling must remain")
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

// TestClobber_UnboundReplacement pins the conformist#100 guard: replacing an
// element with an identifier nothing binds would write a flake referencing an
// UNDEFINED variable. `nix-instantiate --parse` cannot catch that (it is
// syntax-only), so Clobber refuses statically against the parse tree.
func TestClobber_UnboundReplacement(t *testing.T) {
	// Same shape as minimalFlake but WITHOUT the justPkg let binding — i.e. a
	// repo whose additive migration half has not run yet.
	src := `{
  inputs = {
    utils.url = "github:numtide/flake-utils";
    conformist.url = "git+https://code.linenisgreat.com/conformist.git";
  };

  outputs = { self, utils, conformist }:
    utils.lib.eachDefaultSystem (system: let
      pkgs = import <nixpkgs> { inherit system; };
      conformistPkg = conformist.packages.${system}.default;
    in {
      devShells.default = pkgs.mkShell {
        packages = [
          conformistPkg
          pkgs.just
        ];
      };
    });
}
`
	out, _, err := Clobber([]byte(src), []ListElementMigration{{Old: "pkgs.just", New: "justPkg"}})
	require.ErrorIs(t, err, ErrUnboundElement)
	assert.Equal(t, []byte(src), out, "an unbound replacement must not modify src")
}

// TestClobber_BoundReplacementAccepted is the control for the test above: the
// identical migration succeeds once the binding exists.
func TestClobber_BoundReplacementAccepted(t *testing.T) {
	_, report, err := Clobber(
		[]byte(minimalFlake),
		[]ListElementMigration{{Old: "pkgs.just", New: "justPkg"}},
	)
	require.NoError(t, err)
	assert.True(t, report.Changed())
}

// TestClobber_DottedReplacementNotBindingChecked: a dotted path resolves
// through machinery this shallow parser does not model, so it is deliberately
// exempt from the binding check rather than falsely refused.
func TestClobber_DottedReplacementNotBindingChecked(t *testing.T) {
	_, report, err := Clobber(
		[]byte(minimalFlake),
		[]ListElementMigration{{Old: "pkgs.just", New: "pkgs.just_1_36"}},
	)
	require.NoError(t, err)
	assert.True(t, report.Changed())
}

// TestClobber_HybridMergeAccepted: the eng-hybrid is now a recognized shape
// (conformist#65), and its merge side normally holds only system-independent
// outputs, so the per-system packages list is the unambiguous target.
func TestClobber_HybridMergeAccepted(t *testing.T) {
	src := `{
  inputs = {
    utils.url = "github:numtide/flake-utils";
    conformist.url = "git+https://code.linenisgreat.com/conformist.git";
    just-us.url = "git+https://code.linenisgreat.com/just-us.git";
  };

  outputs = { self, utils, conformist, just-us }:
    (utils.lib.eachDefaultSystem (system: let
      pkgs = import <nixpkgs> { inherit system; };
      justPkg = just-us.packages.${system}.default;
    in {
      devShells.default = pkgs.mkShell {
        packages = [
          pkgs.just
        ];
      };
    }))
    // {
      nixosModules.default = ./module.nix;
    };
}
`
	out, report, err := Clobber([]byte(src), []ListElementMigration{{Old: "pkgs.just", New: "justPkg"}})
	require.NoError(t, err)
	assert.True(t, report.Changed())
	assert.Contains(t, string(out), "justPkg")
	assert.NotContains(t, string(out), "pkgs.just")
	// The merge side must survive the rewrite untouched.
	assert.Contains(t, string(out), "nixosModules.default = ./module.nix;")
}

// TestClobber_ShadowedDevShellRefused: when the trailing merge REDEFINES
// devShells, `//` gives it precedence, so the per-system list this tool edits
// is not the one the flake exposes. Rewriting it would be a destructive edit
// with no effect — worse than refusing.
func TestClobber_ShadowedDevShellRefused(t *testing.T) {
	src := `{
  inputs = {
    utils.url = "github:numtide/flake-utils";
    just-us.url = "git+https://code.linenisgreat.com/just-us.git";
  };

  outputs = { self, utils, just-us }:
    utils.lib.eachDefaultSystem (system: let
      pkgs = import <nixpkgs> { inherit system; };
      justPkg = just-us.packages.${system}.default;
    in {
      devShells.default = pkgs.mkShell {
        packages = [
          pkgs.just
        ];
      };
    })
    // {
      devShells.x86_64-linux.default = "overridden";
    };
}
`
	out, _, err := Clobber([]byte(src), []ListElementMigration{{Old: "pkgs.just", New: "justPkg"}})
	require.ErrorIs(t, err, ErrShadowedTarget)
	assert.Equal(t, []byte(src), out, "a shadowed target must not modify src")
}

// TestClobber_LeadingMergeNotShadowing is the control: a LEADING merge is
// overridden BY the per-system body, so it must NOT trigger the refusal.
func TestClobber_LeadingMergeNotShadowing(t *testing.T) {
	src := `{
  inputs = {
    utils.url = "github:numtide/flake-utils";
    just-us.url = "git+https://code.linenisgreat.com/just-us.git";
  };

  outputs = { self, utils, just-us }:
    {
      devShells.x86_64-linux.other = "harmless";
    }
    // utils.lib.eachDefaultSystem (system: let
      pkgs = import <nixpkgs> { inherit system; };
      justPkg = just-us.packages.${system}.default;
    in {
      devShells.default = pkgs.mkShell {
        packages = [
          pkgs.just
        ];
      };
    });
}
`
	_, report, err := Clobber([]byte(src), []ListElementMigration{{Old: "pkgs.just", New: "justPkg"}})
	require.NoError(t, err, "a leading merge cannot shadow the per-system body")
	assert.True(t, report.Changed())
}

// TestClobber_DuplicateElement pins the half-apply refusal: the splice
// machinery addresses ONE byte span, so a list naming the element twice would
// be rewritten in part and stranded in part.
func TestClobber_DuplicateElement(t *testing.T) {
	src := strings.Replace(
		minimalFlake,
		"          pkgs.just\n",
		"          pkgs.just\n          pkgs.just\n",
		1,
	)
	require.Equal(t, 2, strings.Count(src, "pkgs.just"), "fixture should name the element twice")

	out, _, err := Clobber([]byte(src), []ListElementMigration{{Old: "pkgs.just", New: "justPkg"}})
	require.ErrorIs(t, err, ErrDuplicateElement)
	assert.Equal(t, []byte(src), out, "a duplicate element must not modify src")
}

// TestClobber_NotApplicableIsReported: a migration that does not apply must be
// stated, not merely absent. In a 34-repo sweep log, printing nothing is
// indistinguishable from a successful migration.
func TestClobber_NotApplicableIsReported(t *testing.T) {
	_, report, err := Clobber(
		[]byte(minimalFlake),
		[]ListElementMigration{{Old: "pkgs.nonexistent", New: "somethingBound"}},
	)
	require.NoError(t, err)
	assert.False(t, report.Changed())
	require.Len(t, report.NotApplicable, 1)
	assert.Contains(t, report.NotApplicable[0], "pkgs.nonexistent")
}

// TestBuildMigrations_PairingIsStrict pins the destructive-default fix: a
// missing --new must be an error, never an implicit deletion.
func TestBuildMigrations_PairingIsStrict(t *testing.T) {
	t.Run("missing new is an error", func(t *testing.T) {
		_, err := buildMigrations([]string{"pkgs.just"}, nil)
		require.Error(t, err, "a bare --old must not silently become a delete")
	})

	t.Run("too many new is an error", func(t *testing.T) {
		_, err := buildMigrations([]string{"a"}, []string{"x", "y"})
		require.Error(t, err)
	})

	t.Run("no old is an error", func(t *testing.T) {
		_, err := buildMigrations(nil, nil)
		require.Error(t, err)
	})

	t.Run("explicit empty new is a deletion", func(t *testing.T) {
		got, err := buildMigrations([]string{"pkgs.just"}, []string{""})
		require.NoError(t, err)
		require.Equal(t, []ListElementMigration{{Old: "pkgs.just", New: ""}}, got)
	})

	t.Run("paired", func(t *testing.T) {
		got, err := buildMigrations([]string{"a", "b"}, []string{"x", "y"})
		require.NoError(t, err)
		require.Equal(t, []ListElementMigration{{Old: "a", New: "x"}, {Old: "b", New: "y"}}, got)
	})
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
		got := flakeparse.TokenIndex(c.s, c.needle)
		assert.Equal(t, c.want, got, "TokenIndex(%q, %q)", c.s, c.needle)
	}
}
