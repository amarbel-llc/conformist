package flakeparse

import (
	_ "embed"
	"fmt"
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
