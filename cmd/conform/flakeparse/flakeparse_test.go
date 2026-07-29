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
	src := `{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { self, nixpkgs, utils }:
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
}
`
	_, outs, err := flakeparse.ParseFlake([]byte(src))
	require.NoError(t, err, "a redundant wrapping paren must not make the shape match fail")

	assert.True(t, outs.LetExisting["pkgs"])
	require.NotNil(t, outs.DevShellPackages)

	// The splice offsets must still be absolute and correct through the extra
	// paren — that is the part a punctuation change could silently break.
	ls := *outs.DevShellPackages
	assert.Equal(t, byte(']'), src[ls.CloseOff])
	assert.Equal(t, byte('['), src[ls.InnerStart()])
	assert.Contains(t, ls.Inner, "pkgs.just")
}

// TestParseFlakeUnrecognizedShapes pins the refusals. The narrow roster IS the
// safety story for a destructive sweep, so these must keep failing.
func TestParseFlakeUnrecognizedShapes(t *testing.T) {
	cases := map[string]string{
		"not a flake": "# just a comment\n",
		"raw genAttrs": `{
  outputs = { self, nixpkgs }: { packages.x86_64-linux.default = 1; };
}
`,
		"no let in": `{
  outputs = { self, utils }: utils.lib.eachDefaultSystem (system: { x = 1; });
}
`,
		"hybrid //": `{
  outputs =
    { self, utils }:
    { overlays.default = f: p: { }; }
    // utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = 1;
      in
      { packages.default = pkgs; }
    );
}
`,
		// The hybrid's other spelling, with the merge AFTER the call (just-us).
		// Accepting the conformist#101 paren must not accidentally admit this:
		// the trailing `// { … }` puts real outputs outside the per-system body,
		// so splicing into the eachDefaultSystem attrset alone would be wrong.
		"hybrid // trailing": `{
  outputs =
    { self, utils }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = 1;
      in
      { packages.default = pkgs; }
    )
    // {
      lib.thing = ./thing.nix;
    };
}
`,
		// A `rec { … }` per-system return (posh, smith). Structurally splicable,
		// but `rec` makes every sibling attr a binding in scope, so an inserted
		// attr can be captured by — or shadow — a name the repo already defines.
		// Left refused deliberately; see the issue filed alongside #101.
		"rec return attrset": `{
  outputs =
    { self, utils }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = 1;
      in
      rec {
        packages.default = pkgs;
      }
    );
}
`,
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := flakeparse.ParseFlake([]byte(src))
			require.ErrorIs(t, err, flakeparse.ErrUnrecognized)
		})
	}
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

// TestParseFlakeTopLevelWithClause documents a KNOWN GAP, not desired
// behaviour: a `with <expr>;` prefix at the top level of a binding value ends
// the binding at the `with`'s semicolon, exactly like the nested-`let` defect
// did. `Value` stops at the first top-level `;`, and `with pkgs;` puts one
// there before the value proper begins.
//
// Nested inside a group it is fine (TestParseFlakeWithClause below) because
// `Inner` treats `;` as content — which is why this went unnoticed. It is the
// real cause of posh's refusal (two `with pkgs; [ … ]` let bindings), found by
// `just debug-flakeparse-bisect`.
//
// Pinned so the gap is visible and so whoever fixes it sees this test flip.
// The fix is a `WithGroup` alternative in `Value`, mirroring `LetGroup`.
func TestParseFlakeTopLevelWithClause(t *testing.T) {
	src := wrap(
		`        buildInputs = with pkgs; [
          openssl
          zlib
        ];
`,
		`        packages.default = pkgs.hello;
`,
	)

	_, _, err := flakeparse.ParseFlake([]byte(src))
	require.ErrorIs(t, err, flakeparse.ErrUnrecognized,
		"KNOWN GAP: if this now parses, the with-clause defect is fixed — "+
			"delete this test and add a positive one")
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
