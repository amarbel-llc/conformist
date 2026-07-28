package flakeparse

import (
	_ "embed"
	"fmt"

	langlang "github.com/clarete/langlang/go"
)

//go:embed nix.peg
var nixGrammar []byte

//go:embed outputs.peg
var outputsGrammar []byte

const (
	nixEntry     = "nix.peg"
	outputsEntry = "outputs.peg"
)

// newMatcher compiles a CST-mode PEG grammar into a langlang Matcher via
// the in-memory loader. The grammars manage whitespace explicitly via
// Trivia rules, so langlang's automatic Spacing injection is disabled.
//
// Ported from amarbel-llc/doppelgang internal/0/nixedit/walk.go.
//
//nolint:ireturn // langlang.Matcher is the library's own interface type
func newMatcher(entry string, grammar []byte) (langlang.Matcher, error) {
	cfg := langlang.NewConfig()
	cfg.SetBool("grammar.handle_spaces", false)
	loader := langlang.NewInMemoryImportLoader()
	loader.Add(entry, grammar)
	db := langlang.NewDatabase(cfg, loader)

	m, err := langlang.QueryMatcher(db, entry)
	if err != nil {
		return nil, fmt.Errorf("compile grammar %s: %w", entry, err)
	}

	return m, nil
}
