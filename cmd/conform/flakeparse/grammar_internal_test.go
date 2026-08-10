package flakeparse

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGrammarsDoNotShadowSharedRules is the enforced form of the policy
// shared.peg's header states in prose.
//
// It doubles as the POSITIVE CONTROL for the guard: a check that never fires
// looks identical to one that always passes, so the refusal test below only
// proves something if the real grammars are known to reach this cleanly.
func TestGrammarsDoNotShadowSharedRules(t *testing.T) {
	for entry, grammar := range map[string][]byte{
		nixEntry:     nixGrammar,
		outputsEntry: outputsGrammar,
	} {
		require.NoError(t, assertNoShadowedRules(entry, grammar),
			"%s must not locally define a rule shared.peg also defines", entry)
	}
}

// TestAssertNoShadowedRulesDetectsAReintroducedCopy re-creates the exact
// regression the guard exists to catch: someone pasting a lexical rule back
// into an entrypoint to tweak it locally.
//
// langlang accepts that silently and prefers the local copy
// (grammar_ast.go:732 AddDefinition is a no-op when the name already exists,
// and the importing file is parsed first), which is how conformist#106 stayed
// invisible for as long as it did.
func TestAssertNoShadowedRulesDetectsAReintroducedCopy(t *testing.T) {
	// A plausible "I just need OuterText to also stop at `rec`" edit.
	local := []byte(`@import Trivia, Binding from "./shared.peg"

File       <- Trivia Binding* Trivia
OuterText  <- (!(LetKw) !(WithKw) OuterChunk)+
`)

	err := assertNoShadowedRules(nixEntry, local)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "OuterText", "the offending rule must be named")
	assert.Contains(t, err.Error(), sharedEntry, "the fix location must be named")
}

// TestCompileMatcherRefusesAShadowingGrammar pins the WIRING, not the check.
// Testing assertNoShadowedRules directly proves the predicate works; it does
// not prove compileMatcher consults it. Deleting the call site would leave
// every other test in this file green.
func TestCompileMatcherRefusesAShadowingGrammar(t *testing.T) {
	_, err := compileMatcher("shadowing.peg", []byte(`@import Trivia from "./shared.peg"

File       <- Trivia
OuterText  <- .
`))

	require.Error(t, err, "compileMatcher must reject a shadowing grammar, not just be able to detect one")
	assert.Contains(t, err.Error(), "OuterText")
}

// TestAssertNoShadowedRulesIgnoresSharedItself: shared.peg defines every one
// of those rules by definition, so checking it against itself would report the
// whole file as a conflict.
func TestAssertNoShadowedRulesIgnoresSharedItself(t *testing.T) {
	require.NoError(t, assertNoShadowedRules(sharedEntry, sharedGrammar))
}

// TestDefinedRuleNamesReadsDefinitionsOnly pins the scanner against the two
// shapes that would otherwise produce false positives: a rule quoted inside a
// comment (nix.peg's header used to carry several) and a continuation line of
// a multi-line alternation.
func TestDefinedRuleNamesReadsDefinitionsOnly(t *testing.T) {
	names := definedRuleNames([]byte(`// LetSemiChar <- !(LetKw) !(InKw) .
	// Indented comment: OuterText <- nope
Group      <- BraceOpen Inner BraceClose
            / BracketOpen Inner BracketClose
Trivia     <- (WS / Comment)*
`))

	assert.Equal(t,
		[]string{"Group", "Trivia"},
		slices.Sorted(maps.Keys(names)),
		"only first-column definitions count")
}
