package flakeparse_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	flakeparse "code.linenisgreat.com/conformist/cmd/conform/flakeparse"
)

// wrap builds a recognized-shape flake around a per-system let block and
// return attrset, so a test can vary just the part it cares about.
func wrap(letBindings, returnAttrs string) string {
	return `{
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
` + letBindings + `      in
      {
` + returnAttrs + `      }
    );
}
`
}

// nixPegFlake builds a flake whose distinguishing feature sits OUTSIDE the
// eachDefaultSystem call, so it is parsed by nix.peg (the first pass) rather
// than outputs.peg. wrap cannot reach that region: it only varies the
// per-system let block and return attrset, both of which live inside the call
// and are therefore consumed by nix.peg as opaque group content.
//
// This distinction is the whole point of the conformist#106 tests below. The
// fleet corpus proves nothing about them — `just debug-flakeclobber-regression`
// reports newly-accepted=0, because no repo currently combines an outer `let`
// with a keyword-suffixed identifier. Latent is not fixed, and a green sweep
// over a corpus that never exercises the defect is precisely the kind of
// check that passes for an incidental reason.
func nixPegFlake(topLevelExtra, outerLet string) string {
	return `{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
  };

` + topLevelExtra + `  outputs =
    { self, nixpkgs, utils }:
` + outerLet + `    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = [ pkgs.just ];
        };
      }
    );
}
`
}

// TestParseFlakeOuterLetIdentifierEndingInKeyword pins the first half of
// conformist#106 against nix.peg specifically.
//
// nix.peg's LetSemiChar used to test the InKw lookahead at EVERY byte:
//
//	LetSemiChar <- !(LetKw) !(InKw) !('{' / … ) .
//
// `InKw` is `'in' ![a-zA-Z0-9_'\-]`, so in the identifier `bin` the lookahead
// succeeds at index 1 — "in" followed by a space is a complete token as far as
// that rule can tell. LetSemiChar then fails mid-word, every other LetInner
// alternative fails at the same byte, and LetGroup's required InKw happily
// consumes the `in` inside `bin`. The outer let closes early and the file is
// refused.
//
// The fix is structural rather than a ported patch: nix.peg imports Binding
// from shared.peg, so it gets the one definition of LetSemiText — which
// consumes words atomically via OuterWord, meaning the lookahead is only ever
// tested at a token boundary.
//
// `bin` is not a contrived name. It is the single most common identifier of
// this shape in real flakes, alongside plugin, origin, main and
// writeShellScriptBin.
func TestParseFlakeOuterLetIdentifierEndingInKeyword(t *testing.T) {
	src := nixPegFlake("", `    let
      bin = "conformist";
      plugin = "dewey";
      origin = "forge";
    in
`)

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err,
		"an outer-let identifier ending in a keyword must not close the let early")

	require.NotNil(t, outs.DevShellPackages,
		"the second pass must still locate the devShell list")
	assert.Contains(t, outs.DevShellPackages.Inner, "pkgs.just")
}

// TestParseFlakeTopLevelBindingWithClause pins the second half of
// conformist#106 — the divergence the issue does NOT mention.
//
// nix.peg's Value had no WithGroup alternative, so `with <expr>;` in a
// top-level flake binding ended the binding at the `with`'s own semicolon:
// OuterText consumed `with builtins`, stopped at `;`, Value's `*` ended and
// Semi matched. The leftover `{ … };` is not an `AttrPath = Value ;`, so the
// enclosing AttrSet's Binding* stopped, BraceClose found `{`, and File failed.
//
// This is the same defect class as conformist#103, which fixed it one level
// down in outputs.peg and was never ported up. Fixing only the atomic-word
// half would have left the grammars divergent while looking done — which is
// the failure #106 exists to prevent, not an instance of it.
func TestParseFlakeTopLevelBindingWithClause(t *testing.T) {
	src := nixPegFlake(`  nixConfig = with builtins; {
    extra-substituters = [ "https://cache.example.org" ];
  };

`, "")

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err,
		"a top-level `with <expr>;` binding must not terminate at the with's semicolon")

	require.NotNil(t, outs.DevShellPackages,
		"the outputs binding after the with-clause must still be located")
	assert.Contains(t, outs.DevShellPackages.Inner, "pkgs.just")
}

// TestParseFlakeNestedLetIn is the nested-let regression: a binding whose
// VALUE is itself a `let … in …` expression. Its inner `;` are let-binding
// separators, not the outer binding's terminator — before the fix the grammar
// ended the binding at the first nested `;` and the whole shape match failed,
// so ParseFlake (and therefore both conform and flakeclobber) refused the file.
//
// The shape is common: hoisting a version constant read from version.env is the
// eng-versioning(7) idiom.
func TestParseFlakeNestedLetIn(t *testing.T) {
	src := wrap(
		`        version =
          let
            raw = builtins.readFile ./version.env;
          in
          nixpkgs.lib.removePrefix "export VERSION=" raw;
`,
		`        devShells.default = pkgs.mkShell {
          packages = [ pkgs.just ];
        };
`,
	)

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err, "a nested let…in must not make the shape match fail")

	assert.True(t, outs.LetExisting["version"], "the nested-let binding should be named")
	assert.True(t, outs.LetExisting["pkgs"], "sibling bindings must survive")
	require.NotNil(t, outs.DevShellPackages, "the devShell packages list must still be located")
	assert.Contains(t, outs.DevShellPackages.Inner, "pkgs.just")
}

// TestParseFlakeNestedLetInDeep covers a let nested inside a let value, which
// the LetGroup rule must recurse through rather than closing at the first `in`.
func TestParseFlakeNestedLetInDeep(t *testing.T) {
	src := wrap(
		`        version =
          let
            inner =
              let
                base = "1.2.3";
              in
              base;
          in
          inner;
`,
		`        devShells.default = pkgs.mkShell {
          packages = [ pkgs.just ];
        };
`,
	)

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err)
	assert.True(t, outs.LetExisting["version"])
	require.NotNil(t, outs.DevShellPackages)
}

// TestParseFlakeIdentifiersContainingKeywords guards the word-boundary rule in
// OuterText/LetSemiText. The `let`/`in` lookaheads are tested only at token
// boundaries, so an identifier that merely CONTAINS or ENDS IN a keyword must
// not terminate a text run. `bin` is unavoidable in real flakes
// (`${pkg}/bin/foo`, `writeShellScriptBin`), and `outlet`/`applet` end in "let".
func TestParseFlakeIdentifiersContainingKeywords(t *testing.T) {
	src := wrap(
		`        version =
          let
            binPath = "${pkgs.hello}/bin/hello";
            wrapper = pkgs.writeShellScriptBin "x" "exec ${binPath}";
          in
          wrapper;
        outlet = pkgs.hello;
        applet = pkgs.hello;
        plugin = pkgs.hello;
`,
		`        devShells.default = pkgs.mkShell {
          packages = [ pkgs.just ];
        };
`,
	)

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err, "identifiers containing let/in keywords must not break the parse")

	for _, name := range []string{"version", "outlet", "applet", "plugin"} {
		assert.True(t, outs.LetExisting[name], "binding %q should be named", name)
	}
}

// TestParseFlakeParenWrappedCall is conformist#101: a redundant paren around
// the whole eachDefaultSystem application. In Nix a wrapping paren is identity,
// so this is the SAME shape differing only in punctuation — refusing it was a
// parser limitation. It accounted for 5 of the fleet's refusals (dodder,
// madder, moxy, piggy, tacky).
func TestParseFlakeParenWrappedCall(t *testing.T) {
	src := withInputs(`    { self, nixpkgs, utils }:
    (utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.just
          ];
        };
      }
    ));
`)
	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err, "a redundant wrapping paren must not make the shape match fail")

	assert.True(t, outs.LetExisting["pkgs"])

	// The splice offsets must still be absolute and correct through the extra
	// paren — that is the part a punctuation change could silently break.
	assertPackagesOffsets(t, src, outs)
}

// assertPackagesOffsets is the check that actually matters for the hybrid
// (conformist#65): accepting a new shape is worthless if the splice offsets
// drift. A merge expression changes what surrounds the per-system attrset, and
// the offsets are ABSOLUTE in the original source, so this asserts the located
// span is byte-exact rather than merely non-nil.
func assertPackagesOffsets(t *testing.T, src string, outs flakeparse.ParsedOutputs) { //testui:allow // testify helper
	t.Helper()

	require.NotNil(t, outs.DevShellPackages, "the packages list must be located")

	ls := *outs.DevShellPackages
	assert.Equal(t, byte('['), src[ls.InnerStart()], "InnerStart must point at the opening bracket")
	assert.Equal(t, byte(']'), src[ls.CloseOff], "CloseOff must point at the closing bracket")
	assert.Equal(t, ls.Inner, src[ls.InnerStart():ls.CloseOff+1],
		"Inner must be exactly the source span it claims")
	assert.Contains(t, ls.Inner, "pkgs.just")

	// The return attrset's closing brace is the other splice target.
	assert.Equal(t, byte('}'), src[outs.RetCloseOff],
		"RetCloseOff must point at the return attrset's closing brace")
}

// hybridBody is the per-system half every hybrid case below shares.
const hybridBody = `      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        formatter = pkgs.nixfmt;
        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.just
          ];
        };
      }
`

// TestParseFlakeHybridMerge is conformist#65: the eng-hybrid `//` merge, in
// BOTH spellings and composed with the conformist#101 paren. It was 9 of 17
// fleet refusals. Each case asserts the splice offsets, not just acceptance.
func TestParseFlakeHybridMerge(t *testing.T) {
	cases := map[string]struct {
		outputs  string
		trailing bool
	}{
		// circus: merge attrset LEADS the call. The per-system body overrides
		// it, so a collision here cannot shadow the wiring.
		"leading merge": {outputs: `    {
      nixosModules.default = ./module.nix;
    }
    // utils.lib.eachDefaultSystem (
` + hybridBody + `    )`},

		// just-us: merge attrset TRAILS the call.
		"trailing merge": {outputs: `    utils.lib.eachDefaultSystem (
` + hybridBody + `    )
    // {
      lib.conformistLinters = ./linters.nix;
    }`, trailing: true},

		// piggy/dodder: paren-wrapped AND trailing-merged — the two optionals
		// must compose.
		"paren-wrapped trailing merge": {outputs: `    (utils.lib.eachDefaultSystem (
` + hybridBody + `    ))
    // {
      nixosModules.piggy-agent = ./nixos.nix;
    }`, trailing: true},

		// circus again: an OUTER let before the leading merge.
		"outer let with leading merge": {outputs: `    let
      helper = x: x;
    in
    {
      nixosModules.default = ./module.nix;
    }
    // utils.lib.eachDefaultSystem (
` + hybridBody + `    )`},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			src := `{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { self, nixpkgs, utils }:
` + c.outputs + `;
}
`
			_, outs, err := flakeparse.ParseFlake([]byte(src))
			require.NoError(t, err, "the eng-hybrid must be recognized")

			assert.True(t, outs.LetExisting["pkgs"], "per-system let must still be read")
			assert.True(t, outs.RetExisting["formatter"], "per-system attrs must still be read")
			assert.Equal(t, c.trailing, outs.MergeIsTrailing,
				"the merge direction decides whether a collision shadows")
			assertPackagesOffsets(t, src, outs)
		})
	}
}

// TestParseFlakeHybridMergeSideAttrsRecorded pins the shadowing guard. `//`
// gives the RIGHT operand precedence, so an attr on a TRAILING merge side
// overrides the same attr spliced into the per-system body — wiring that
// reports success and does nothing. The names must be recovered so the caller
// can refuse instead.
func TestParseFlakeHybridMergeSideAttrsRecorded(t *testing.T) {
	src := withInputs(`    { self, nixpkgs, utils }:
    utils.lib.eachDefaultSystem (
` + hybridBody + `    )
    // {
      nixosModules.default = ./module.nix;
      formatter = pkgs.alejandra;
    };
`)
	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err)

	assert.True(t, outs.MergeIsTrailing)
	assert.True(t, outs.MergeExisting["nixosModules.default"],
		"merge-side attrs must be recovered")
	assert.True(t, outs.MergeExisting["formatter"],
		"a shadowing collision must be visible to the caller")
	assert.False(t, outs.MergeExisting["devShells.default"],
		"absent merge-side attrs must not be reported")
}

// withInputs wraps an outputs value in a flake that HAS an inputs attrset.
//
// This matters more than it looks. ParseFlake requires FindInputsAttrSet to
// succeed, so a fixture with no `inputs` block refuses no matter what its
// outputs look like — a refusal-roster case written without one passes
// vacuously and never exercises the shape it claims to pin. An earlier version
// of this test had exactly that bug across every case.
func withInputs(outputs string) string {
	return `{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
` + outputs + `}
`
}

// TestParseFlakeAtPattern is conformist#104: an at-pattern binding the whole
// argument attrset to a name, in BOTH spellings. Another pure spelling
// difference — the call, the per-system body and every splice target are
// identical, only the argument is written differently. Frees just-us.
func TestParseFlakeAtPattern(t *testing.T) {
	cases := map[string]string{
		// just-us's spelling.
		"trailing @inputs": `    {
      self,
      nixpkgs,
      utils,
      ...
    }@inputs:
`,
		// The other spelling Nix allows.
		"leading inputs@": `    inputs@{
      self,
      nixpkgs,
      utils,
      ...
    }:
`,
	}

	for name, argPattern := range cases {
		t.Run(name, func(t *testing.T) {
			src := withInputs(argPattern + `    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.just
          ];
        };
      }
    );
`)
			_, outs, err := flakeparse.ParseFlake([]byte(src))
			require.NoError(t, err, "an at-pattern argument must not fail the shape match")

			assert.True(t, outs.LetExisting["pkgs"])
			assertPackagesOffsets(t, src, outs)

			// The destructured formals must still be readable — they decide
			// whether conform has to splice `conformist`/`just-us` args in.
			assert.True(t, outs.ArgNames["self"])
			assert.True(t, outs.ArgNames["nixpkgs"])
			assert.False(t, outs.ArgNames["conformist"], "an absent formal must not be reported")

			// The at-pattern's own name shares the formal namespace: Nix rejects
			// `{ inputs, … }@inputs:` as a duplicate formal, so conform must see
			// it as taken.
			assert.True(t, outs.ArgNames["inputs"],
				"the at-pattern name must be registered so a splice cannot collide with it")

			// The argument splice still lands just inside the brace.
			assert.Equal(t, byte('{'), src[outs.ArgInsertOff-1],
				"ArgInsertOff must sit immediately after the argset's opening brace")
		})
	}
}

// TestParseFlakeChainedLet is conformist#105: sibling `let … in let … in`
// blocks before the returned attrset. Distinct from the nested let (which sits
// inside a binding VALUE); these are siblings, a normal way to stage bindings
// that depend on an earlier block. Frees clown.
func TestParseFlakeChainedLet(t *testing.T) {
	src := withInputs(
		`    { self, nixpkgs, utils }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        conformistPkg = pkgs.hello;
      in
      let
        lib = pkgs.lib;
      in
      let
        third = lib.id 1;
      in
      {
        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.just
          ];
        };
      }
    );
`,
	)

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err, "chained sibling let blocks must not fail the shape match")

	// LetExisting must be the UNION across every block. Reading only the first
	// (or only the last) would let the idempotency sentinel misfire: a flake
	// whose conformist bindings live in an earlier block would look unwired and
	// get a duplicate set spliced in.
	for _, name := range []string{"pkgs", "conformistPkg", "lib", "third"} {
		assert.True(t, outs.LetExisting[name],
			"binding %q from a chained block must be in the union", name)
	}

	// The splice point must be the LAST `in`, so a new binding can reference
	// anything bound in an earlier block.
	assert.Equal(t, "in", src[outs.LetCloseOff:outs.LetCloseOff+2],
		"LetCloseOff must point at an `in` keyword")
	assert.Greater(t, outs.LetCloseOff, strings.Index(src, "third = "),
		"LetCloseOff must be the LAST `in`, after every chained block")

	assertPackagesOffsets(t, src, outs)
}

// TestParseFlakeUnrecognizedShapes pins the refusals. The narrow roster IS the
// safety story for a destructive sweep, so widening it for the paren
// (conformist#101) or the hybrid (conformist#65) must not drag these in too.
//
// Every case carries a real inputs block, so the refusal is attributable to
// the OUTPUTS shape and nothing else.
func TestParseFlakeUnrecognizedShapes(t *testing.T) {
	cases := map[string]string{
		"raw genAttrs": withInputs(
			`    { self, nixpkgs, utils }:
    {
      packages = nixpkgs.lib.genAttrs [ "x86_64-linux" ] (system: { default = 1; });
    };
`,
		),

		"forAllSystems": withInputs(
			`    { self, nixpkgs, utils }:
    let
      forAllSystems = nixpkgs.lib.genAttrs [ "x86_64-linux" ];
    in
    {
      devShells = forAllSystems (system: { default = 1; });
    };
`,
		),

		"flake-parts mkFlake": withInputs(
			`    inputs@{ self, nixpkgs, utils, flake-parts }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [ "x86_64-linux" ];
      perSystem =
        { pkgs, ... }:
        {
          packages.default = pkgs.hello;
        };
    };
`,
		),

		// eachSystem, not eachDefaultSystem (pa6e). A different function with a
		// different arity — not the recognized call.
		"eachSystem variant": withInputs(
			`    { self, nixpkgs, utils }:
    utils.lib.eachSystem [ "x86_64-linux" ] (
      system:
      let
        pkgs = 1;
      in
      {
        packages.default = pkgs;
      }
    );
`,
		),

		"no let in": withInputs(
			`    { self, nixpkgs, utils }:
    utils.lib.eachDefaultSystem (system: { packages.default = 1; });
`,
		),

		// A `rec { … }` per-system return (smith). Structurally splicable, but
		// `rec` puts every sibling attr in scope, so an inserted attr could be
		// captured by — or shadow — a name the repo already binds. That is a real
		// mis-splice, so it stays refused: "we could splice it" is not "it is
		// safe to".
		"rec return attrset": withInputs(
			`    { self, nixpkgs, utils }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = 1;
      in
      rec {
        packages.default = pkgs;
      }
    );
`,
		),
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := flakeparse.ParseFlake([]byte(src))
			require.ErrorIs(t, err, flakeparse.ErrUnrecognized)
		})
	}

	// Guard the guard: the wrapper itself must produce a RECOGNIZED flake, so a
	// case above cannot pass merely because withInputs emits something broken.
	t.Run("control: wrapper alone is recognized", func(t *testing.T) {
		src := withInputs(
			`    { self, nixpkgs, utils }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages.default = pkgs.hello;
      }
    );
`,
		)
		_, _, err := flakeparse.ParseFlake([]byte(src))
		require.NoError(t, err, "the control must parse, or the refusals above prove nothing")
	})

	t.Run("not a flake", func(t *testing.T) {
		_, _, err := flakeparse.ParseFlake([]byte("# just a comment\n"))
		require.ErrorIs(t, err, flakeparse.ErrUnrecognized)
	})
}

// TestParseFlakeLocatesDevShellPackages covers the navigation entry point
// flakeclobber depends on: the packages list must be located with offsets
// ABSOLUTE in the original source, since the splice addresses raw bytes.
func TestParseFlakeLocatesDevShellPackages(t *testing.T) {
	src := wrap("", `        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.just
            pkgs.go
          ];
        };
`)

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err)
	require.NotNil(t, outs.DevShellPackages)

	ls := *outs.DevShellPackages
	assert.Equal(t, byte(']'), src[ls.CloseOff], "CloseOff must point at the closing bracket")
	assert.Equal(t, byte('['), src[ls.InnerStart()], "InnerStart must point at the opening bracket")
	assert.Equal(t, ls.Inner, src[ls.InnerStart():ls.CloseOff+1],
		"Inner must be exactly the source span it claims")
}

// TestParseFlakeTopLevelWithClause is conformist#103: a `with <expr>;` prefix
// at the top level of a binding value. Its semicolon belongs to the `with`,
// not to the binding — before WithGroup, Value ended the binding at
// `with pkgs;` and the leftover `[ … ];` failed the shape match.
//
// Nested inside a group it always worked (TestParseFlakeWithClause below)
// because `Inner` treats `;` as content — which is why this went unnoticed.
// It was the real cause of posh's refusal (two `with pkgs; [ … ]` let
// bindings), found by `just debug-flakeparse-bisect`.
func TestParseFlakeTopLevelWithClause(t *testing.T) {
	src := wrap(
		`        buildInputs = with pkgs; [
          openssl
          zlib
        ];
        nativeBuildInputs =
          with pkgs;
          [
            pkg-config
          ];
`,
		`        packages.default = pkgs.hello;
        devShells.default = pkgs.mkShell {
          packages = [ pkgs.just ];
        };
`,
	)

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err, "a top-level with-clause must not end the binding early")

	assert.True(t, outs.LetExisting["buildInputs"], "the with-clause binding should be named")
	assert.True(t, outs.LetExisting["nativeBuildInputs"], "the multi-line form too")
	assert.True(t, outs.LetExisting["pkgs"], "sibling bindings must survive")
	require.NotNil(t, outs.DevShellPackages)
	assert.Contains(t, outs.DevShellPackages.Inner, "pkgs.just")
}

// TestParseFlakeIdentifiersEndingInWith guards the word-boundary rule for the
// new WithKw lookahead — the same trap LetKw/InKw needed. An identifier merely
// containing or ending in "with" must not stop a text run mid-word.
func TestParseFlakeIdentifiersEndingInWith(t *testing.T) {
	src := wrap(
		`        wrapped = pkgs.runcommandwith "x";
        helper = pkgs.python3.withPackages (ps: [ ps.requests ]);
`,
		`        packages.default = pkgs.hello;
`,
	)

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err, "identifiers containing/ending in 'with' must not break the parse")
	assert.True(t, outs.LetExisting["wrapped"])
	assert.True(t, outs.LetExisting["helper"])
}

// TestParseFlakeWithClause covers `packages = with pkgs; [ … ]`, which the
// packages-assignment regex has to see through. Note the `with` here is nested
// inside `pkgs.mkShell { … }`, where `;` is group content — contrast with
// TestParseFlakeTopLevelWithClause above.
func TestParseFlakeWithClause(t *testing.T) {
	src := wrap("", `        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            just
            go
          ];
        };
`)

	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err)
	require.NotNil(t, outs.DevShellPackages)
	assert.Contains(t, outs.DevShellPackages.Inner, "just")
}

func TestTokenIndices(t *testing.T) {
	cases := []struct {
		name   string
		s      string
		needle string
		want   []int
	}{
		{"single", "  pkgs.just\n", "pkgs.just", []int{2}},
		{"none", "justPkg\n", "pkgs.just", nil},
		{"embedded is not a token", "  pkgs.justmore\n", "pkgs.just", nil},
		{"prefixed is not a token", "  morepkgs.just\n", "pkgs.just", nil},
		{"two occurrences", "  pkgs.just\n  pkgs.just\n", "pkgs.just", []int{2, 14}},
		{"empty needle", "anything", "", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, flakeparse.TokenIndices(c.s, c.needle))

			// TokenIndex must stay the first element of TokenIndices.
			want := -1
			if len(c.want) > 0 {
				want = c.want[0]
			}
			assert.Equal(t, want, flakeparse.TokenIndex(c.s, c.needle))
		})
	}
}

func TestLineHelpers(t *testing.T) {
	src := []byte("alpha\n    beta\ngamma\n")
	betaOff := strings.Index(string(src), "beta")

	assert.Equal(t, 6, flakeparse.LineStart(src, betaOff))
	assert.Equal(t, "    ", flakeparse.LineIndent(src, betaOff))
	assert.False(t, flakeparse.OnlyBlankBefore(src, betaOff+2),
		"there is non-blank text before the offset")
	assert.True(t, flakeparse.OnlyBlankBefore(src, betaOff),
		"only indentation precedes the token")
}
