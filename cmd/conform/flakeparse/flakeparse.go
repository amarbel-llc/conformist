// Package flakeparse is the shared PEG-parse infrastructure for flake.nix
// surgery. It exposes the parse types (InputsAttrSet, ParsedOutputs,
// ListSplice, ValueRange, Splice) and the single ParseFlake entry point
// used by both flakeedit (incremental wiring) and flakeclobber (fleet
// migration sweeps).
package flakeparse

import (
	"errors"
	"fmt"
)

// ErrUnrecognized means the flake.nix is not the recognized
// eachDefaultSystem shape, or its existing content cannot be navigated
// safely. Callers fall back to print-only or skip-and-report.
var ErrUnrecognized = errors.New("flakeparse: flake.nix is not the recognized eachDefaultSystem shape")

// ParseFlake parses src as a flake.nix and returns the editable inputs
// region and located outputs splice points. Returns ErrUnrecognized when
// the file does not match the recognized eachDefaultSystem shape.
func ParseFlake(src []byte) (InputsAttrSet, ParsedOutputs, error) {
	matcher, err := newMatcher(nixEntry, nixGrammar)
	if err != nil {
		return InputsAttrSet{}, ParsedOutputs{}, fmt.Errorf("flakeparse: compile grammar: %w", err)
	}
	tree, _, err := matcher.Match(src)
	if err != nil {
		return InputsAttrSet{}, ParsedOutputs{}, ErrUnrecognized
	}

	ins, ok := FindInputsAttrSet(tree, src)
	if !ok {
		return InputsAttrSet{}, ParsedOutputs{}, ErrUnrecognized
	}

	valStart, valEnd, ok := outputsValueSpan(tree)
	if !ok {
		return InputsAttrSet{}, ParsedOutputs{}, ErrUnrecognized
	}

	outs, ok := parseOutputs(src, valStart, valEnd)
	if !ok {
		return InputsAttrSet{}, ParsedOutputs{}, ErrUnrecognized
	}

	return ins, outs, nil
}
