package flakeparse

import (
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	langlang "github.com/clarete/langlang/go"
)

//go:embed nix.peg
var nixGrammar []byte

//go:embed outputs.peg
var outputsGrammar []byte

// sharedGrammar holds the lexical rules both passes import by name. It is
// never an entrypoint — it is registered with the loader so `@import … from
// "./shared.peg"` resolves (conformist#106).
//
//go:embed shared.peg
var sharedGrammar []byte

const (
	nixEntry     = "nix.peg"
	outputsEntry = "outputs.peg"
	// sharedEntry must match the import path written in nix.peg and
	// outputs.peg: NewInMemoryImportLoader resolves "./shared.peg" against
	// the importing module to this key.
	sharedEntry = "shared.peg"
)

// Grammars are embedded constants; compile each once per process lifetime.
var (
	nixMatcherOnce = sync.OnceValues(func() (langlang.Matcher, error) {
		return compileMatcher(nixEntry, nixGrammar)
	})
	outputsMatcherOnce = sync.OnceValues(func() (langlang.Matcher, error) {
		return compileMatcher(outputsEntry, outputsGrammar)
	})
)

// compileMatcher compiles a CST-mode PEG grammar into a langlang Matcher via
// the in-memory loader. The grammars manage whitespace explicitly via
// Trivia rules, so langlang's automatic Spacing injection is disabled.
//
// Ported from amarbel-llc/doppelgang internal/0/nixedit/walk.go.
//
//nolint:ireturn // langlang.Matcher is the library's own interface type
func compileMatcher(entry string, grammar []byte) (langlang.Matcher, error) {
	if err := assertNoShadowedRules(entry, grammar); err != nil {
		return nil, err
	}

	if err := assertMirroredRules(entry, grammar); err != nil {
		return nil, err
	}

	cfg := langlang.NewConfig()
	cfg.SetBool("grammar.handle_spaces", false)
	loader := langlang.NewInMemoryImportLoader()
	loader.Add(entry, grammar)
	loader.Add(sharedEntry, sharedGrammar)
	db := langlang.NewDatabase(cfg, loader)

	m, err := langlang.QueryMatcher(db, entry)
	if err != nil {
		return nil, fmt.Errorf("compile grammar %s: %w", entry, err)
	}

	return m, nil
}

// ruleDefinition matches a grammar rule's DEFINITION site: a name in the first
// column followed by langlang's `<-` arrow. A continuation line (`/ Alt`) and
// a reference inside another rule's body are both indented, so neither
// matches.
var ruleDefinition = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*<-`)

// definedRuleNames returns the rule names a grammar file defines itself.
func definedRuleNames(grammar []byte) map[string]struct{} {
	names := map[string]struct{}{}

	for line := range strings.SplitSeq(string(grammar), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}

		if m := ruleDefinition.FindStringSubmatch(line); m != nil {
			names[m[1]] = struct{}{}
		}
	}

	return names
}

// assertNoShadowedRules refuses to compile an entrypoint that locally defines
// a rule shared.peg also defines.
//
// This exists because langlang resolves the collision SILENTLY, in the wrong
// direction. resolveImportsRecursive parses the importing file first, then
// folds each imported definition in via GrammarNode.AddDefinition, which is a
// no-op when the name is already present (langlang/go@v0.0.12
// grammar_ast.go:732). So a local rule wins and the imported one is discarded,
// with no error and no warning.
//
// That is precisely how conformist#106 happened: two copies of a lexical rule,
// nothing announcing that they had stopped agreeing. Factoring the rules into
// shared.peg removed today's copies, but on its own it only replaced the old
// "keep the copies in sync" convention with a new "do not re-add a copy" one —
// still prose, still unenforced, still silent when violated. This check is what
// makes the policy hold: re-adding a local copy now fails the parse loudly
// instead of shadowing its way back to the same bug.
func assertNoShadowedRules(entry string, grammar []byte) error {
	if entry == sharedEntry {
		return nil
	}

	shared := definedRuleNames(sharedGrammar)

	var shadowed []string

	for name := range definedRuleNames(grammar) {
		if _, ok := shared[name]; ok {
			shadowed = append(shadowed, name)
		}
	}

	if len(shadowed) == 0 {
		return nil
	}

	slices.Sort(shadowed)

	return fmt.Errorf(
		"grammar %s locally defines %d rule(s) that %s also defines: %s. "+
			"langlang would silently prefer the local copy and discard the "+
			"imported one, reintroducing the divergence shared.peg exists to "+
			"prevent (conformist#106). Edit the rule in %s instead",
		entry, len(shadowed), sharedEntry, strings.Join(shadowed, ", "), sharedEntry,
	)
}

// mirroredRule names a rule an entrypoint must keep textually identical to one
// in shared.peg. It exists for the one case assertNoShadowedRules cannot cover:
// outputs.peg needs AtName to be a capture node of its own, which a rule that
// merely forwards to Identifier would not give it, so the character class is
// written out twice and nothing but this guard keeps the copies in step
// (conformist#108).
type mirroredRule struct {
	entry  string
	local  string
	shared string
}

var mirroredRules = []mirroredRule{
	{entry: outputsEntry, local: "AtName", shared: "Identifier"},
}

// assertMirroredRules refuses an entrypoint whose mirrored rule has drifted
// from its shared.peg original, or has gone missing (a rename would otherwise
// make the comparison pass vacuously).
func assertMirroredRules(entry string, grammar []byte) error {
	for _, m := range mirroredRules {
		if m.entry != entry {
			continue
		}

		local, ok := ruleBody(grammar, m.local)
		if !ok {
			return fmt.Errorf("grammar %s no longer defines %s, which must mirror %s's %s (conformist#108)",
				entry, m.local, sharedEntry, m.shared)
		}

		shared, ok := ruleBody(sharedGrammar, m.shared)
		if !ok {
			return fmt.Errorf("%s no longer defines %s, which %s's %s must mirror (conformist#108)",
				sharedEntry, m.shared, entry, m.local)
		}

		if local != shared {
			return fmt.Errorf("%s's %s <- %s has drifted from %s's %s <- %s; "+
				"the two copies must stay identical, so edit them together (conformist#108)",
				entry, m.local, local, sharedEntry, m.shared, shared)
		}
	}

	return nil
}

// ruleBody returns the named rule's body with layout collapsed to single
// spaces, so a multi-line alternation compares equal to the same text on one
// line. The second result is false when the grammar does not define the rule.
func ruleBody(grammar []byte, name string) (string, bool) {
	var (
		body  []string
		found bool
	)

	for line := range strings.SplitSeq(string(grammar), "\n") {
		trimmed := strings.TrimSpace(line)

		if found {
			// A continuation is indented and is not a comment; anything else
			// (a definition, a blank line, a comment) ends the rule.
			if trimmed == "" || strings.HasPrefix(trimmed, "//") || line[0] != ' ' && line[0] != '\t' {
				break
			}

			body = append(body, trimmed)

			continue
		}

		if m := ruleDefinition.FindStringSubmatch(line); m != nil && m[1] == name {
			found = true

			_, rest, _ := strings.Cut(line, "<-")
			body = append(body, strings.TrimSpace(rest))
		}
	}

	if !found {
		return "", false
	}

	return strings.Join(strings.Fields(strings.Join(body, " ")), " "), true
}
