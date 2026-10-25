package flakeparse

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMirroredRulesAgree is the POSITIVE CONTROL for the mirror guard: the real
// grammars must pass it, or the drift tests below prove nothing (conformist#108).
func TestMirroredRulesAgree(t *testing.T) {
	require.NoError(t, assertMirroredRules(outputsEntry, outputsGrammar))
	require.NoError(t, assertMirroredRules(nixEntry, nixGrammar),
		"an entrypoint with no mirrored rules has nothing to disagree about")
}

// TestAssertMirroredRulesDetectsDrift edits one side of the AtName/Identifier
// pair the way a well-meaning change to Identifier's character class would, and
// requires the guard to name both rules.
func TestAssertMirroredRulesDetectsDrift(t *testing.T) {
	drifted := []byte(`@import Trivia from "./shared.peg"

AtName <- #([a-zA-Z_] [a-zA-Z0-9_'\-.]*)
`)

	err := assertMirroredRules(outputsEntry, drifted)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "AtName")
	assert.Contains(t, err.Error(), "Identifier")
	assert.Contains(t, err.Error(), sharedEntry, "the file to edit must be named")
}

// TestAssertMirroredRulesRefusesAMissingMirror: a renamed rule must fail loudly
// rather than make the comparison vacuously pass.
func TestAssertMirroredRulesRefusesAMissingMirror(t *testing.T) {
	err := assertMirroredRules(outputsEntry, []byte("File <- .\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "AtName")
}

// TestCompileMatcherRefusesADriftedMirror pins the WIRING: the predicate alone
// does not prove compileMatcher consults it.
func TestCompileMatcherRefusesADriftedMirror(t *testing.T) {
	_, err := compileMatcher(outputsEntry, []byte(`@import Trivia from "./shared.peg"

File   <- Trivia
AtName <- #([a-z]+)
`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "AtName")
}

// TestRuleBodyJoinsContinuationLines: a multi-line alternation is one body, and
// layout differences between the two sides must not count as drift.
func TestRuleBodyJoinsContinuationLines(t *testing.T) {
	body, ok := ruleBody([]byte("Name   <- A\n         / B\nOther  <- C\n"), "Name")

	require.True(t, ok)
	assert.Equal(t, "A / B", body)

	_, ok = ruleBody([]byte("Name <- A\n"), "Missing")
	assert.False(t, ok)
}
