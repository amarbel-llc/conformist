package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	flakeparse "code.linenisgreat.com/conformist/cmd/conform/flakeparse"
)

// ErrUnrecognized is re-exported from flakeparse for callers that don't
// import flakeparse directly.
var ErrUnrecognized = flakeparse.ErrUnrecognized

// ErrPartialState is returned when some migrations are already satisfied
// and others are still pending. No edits are attempted; the operator must
// inspect and resolve.
var ErrPartialState = errors.New("flakeclobber: partial migration state (some entries satisfied, some pending)")

// ErrNoDevShell is returned when the recognized shape has no locatable
// devShells.default packages list.
var ErrNoDevShell = errors.New("flakeclobber: no devShells.default packages list found")

// ErrDuplicateElement is returned when Old occurs more than once in the
// packages list. The splice machinery addresses a single byte span, so
// migrating such a list would rewrite the first occurrence and leave the
// rest — a half-applied destructive edit. Refusing keeps the tool's
// all-or-nothing contract; the operator dedupes by hand.
var ErrDuplicateElement = errors.New("flakeclobber: element occurs more than once in packages list")

// ErrShadowedTarget is returned when the flake is an eng-hybrid whose trailing
// `//` merge redefines devShells. `//` gives its right operand precedence, so
// the per-system packages list this tool edits is not the one the flake
// actually exposes; rewriting it would be a destructive edit with no effect.
var ErrShadowedTarget = errors.New(
	"flakeclobber: devShells is redefined on the trailing // merge side, " +
		"so the per-system packages list is shadowed",
)

// ErrUnboundElement is returned when New is a bare identifier that nothing in
// the flake binds — neither a per-system `let` binding nor an outputs
// argument.
//
// This is the conformist#100 hazard made non-silent. The fleet migration has
// an additive half (the `just-us` input + the `justPkg` let binding, conform's
// job) and a destructive half (this tool). Run destructive-first and the
// rewrite yields a flake referencing an UNDEFINED variable — and
// `nix-instantiate --parse` cannot catch it, because --parse is syntax-only
// and accepts a clobbered flake with no `just-us` and no `justPkg` binding.
//
// Checking the binding statically against the parse tree is exact for this
// failure mode and costs nothing: no network, no flake lock, no toolchain,
// microseconds per file. An eval-level `nix eval` gate would also catch it,
// but across ~34 repos it needs every input fetched and would conflate
// unrelated eval errors with the migration — so it is the wrong instrument
// for a precondition this cheap to prove.
var ErrUnboundElement = errors.New("flakeclobber: replacement identifier is not bound in the flake")

// ListElementMigration describes one list-element substitution within the
// devShells.default packages list.
type ListElementMigration struct {
	// Old is the exact element text to search for (e.g. "pkgs.just").
	Old string
	// New is the replacement text (e.g. "justPkg"). Empty means deletion.
	New string
}

// ClobberReport summarizes the result of Clobber for one file.
type ClobberReport struct {
	// Applied lists each migration that was applied.
	Applied []string
	// Satisfied lists each migration that was already done.
	Satisfied []string
	// NotApplicable lists each migration whose Old and New are both absent,
	// so it does not apply to this file. Recorded explicitly so a sweep over
	// ~34 repos reports "did not apply" instead of printing nothing, which in
	// a run log is indistinguishable from a successful migration.
	NotApplicable []string
}

// Changed reports whether Clobber produced a rewritten source.
func (r ClobberReport) Changed() bool { return len(r.Applied) > 0 }

// elementStatus categorizes a migration entry relative to the current list.
type elementStatus int

const (
	elementPending   elementStatus = iota // Old present, New absent → needs migration
	elementSatisfied                      // Old absent, New present (or deleted) → done
	elementUnknown                        // neither Old nor New found → N/A
	elementConflict                       // both Old and New found → ambiguous
)

// checkElement determines the status of one migration entry within inner.
func checkElement(inner string, m ListElementMigration) elementStatus {
	hasOld := flakeparse.TokenIndex(inner, m.Old) >= 0
	if m.New == "" {
		// Deletion: satisfied when Old is absent.
		if hasOld {
			return elementPending
		}

		return elementSatisfied
	}
	hasNew := flakeparse.TokenIndex(inner, m.New) >= 0
	switch {
	case hasOld && hasNew:
		return elementConflict
	case hasOld:
		return elementPending
	case hasNew:
		return elementSatisfied
	default:
		return elementUnknown
	}
}

// Clobber applies migrations to src and returns the rewritten source plus a
// report. The recognized shape is flakeparse's eachDefaultSystem.
//
// Error returns:
//   - flakeparse.ErrUnrecognized: file is not the recognized shape
//   - ErrNoDevShell: shape recognized but no devShells.default packages list
//   - ErrPartialState: some migrations satisfied, some pending — no edits applied
//   - other non-nil error: unexpected element state or operational failure
func Clobber(src []byte, migrations []ListElementMigration) ([]byte, ClobberReport, error) {
	_, outs, err := flakeparse.ParseFlake(src)
	if err != nil {
		return src, ClobberReport{}, fmt.Errorf("flakeclobber: %w", err)
	}

	// An eng-hybrid whose TRAILING `//` merge redefines devShells overrides the
	// per-system list this tool edits, so a rewrite here would change bytes
	// that no longer affect the flake's outputs — a destructive edit with no
	// effect, which is worse than refusing (conformist#65).
	if outs.MergeShadows("devShells.default") {
		return src, ClobberReport{}, ErrShadowedTarget
	}

	if outs.DevShellPackages == nil {
		return src, ClobberReport{}, ErrNoDevShell
	}

	ls := *outs.DevShellPackages

	// Categorize each migration, collecting the pending ones as we go rather
	// than tagging every entry and re-filtering for the same predicate twice
	// below. The satisfied count is just len(report.Satisfied).
	var (
		report  ClobberReport
		pending []ListElementMigration
	)

	for _, m := range migrations {
		switch checkElement(ls.Inner, m) {
		case elementPending:
			pending = append(pending, m)
		case elementSatisfied:
			if m.New != "" {
				report.Satisfied = append(
					report.Satisfied,
					fmt.Sprintf("%q already replaced with %q", m.Old, m.New),
				)
			} else {
				report.Satisfied = append(report.Satisfied, fmt.Sprintf("%q already removed", m.Old))
			}
		case elementUnknown:
			// N/A: neither Old nor New found; migration doesn't apply to this
			// file. Do not count toward partial-state detection, but do record
			// it so the sweep log says so rather than staying silent.
			report.NotApplicable = append(
				report.NotApplicable,
				fmt.Sprintf("%q not present — migration does not apply", m.Old),
			)
		case elementConflict:
			return src, ClobberReport{}, fmt.Errorf(
				"flakeclobber: both %q and %q present in packages list — ambiguous state",
				m.Old, m.New,
			)
		}
	}

	// All satisfied/N/A → idempotent no-op.
	if len(pending) == 0 {
		return src, report, nil
	}

	// Mixed satisfied + pending → partial state, refuse all edits.
	if len(report.Satisfied) > 0 {
		return src, ClobberReport{}, ErrPartialState
	}

	// Preconditions checked BEFORE any splice is built, so a refusal leaves
	// src untouched.
	for _, m := range pending {
		// A duplicated element cannot be migrated by a single-span splice:
		// rewriting the first occurrence would strand the rest.
		if n := len(flakeparse.TokenIndices(ls.Inner, m.Old)); n > 1 {
			return src, ClobberReport{}, fmt.Errorf(
				"%w: %q appears %d times", ErrDuplicateElement, m.Old, n,
			)
		}

		if err := checkBound(outs, m.New); err != nil {
			return src, ClobberReport{}, err
		}
	}

	// Build splices and apply highest-offset first so earlier offsets stay
	// valid (same invariant as flakeedit.Apply).
	splices := make([]flakeparse.Splice, 0, len(pending))

	for _, m := range pending {
		sp, ok := listElementSplice(src, ls, m)
		if !ok {
			return src, ClobberReport{}, fmt.Errorf("flakeclobber: could not locate span for %q", m.Old)
		}

		splices = append(splices, sp)

		if m.New == "" {
			report.Applied = append(report.Applied, fmt.Sprintf("removed %q", m.Old))
		} else {
			report.Applied = append(report.Applied, fmt.Sprintf("replaced %q with %q", m.Old, m.New))
		}
	}

	sort.Slice(splices, func(i, j int) bool { return splices[i].Offset > splices[j].Offset })
	out := src
	for _, s := range splices {
		out = s.ApplyTo(out)
	}

	return out, report, nil
}

// checkBound verifies that a replacement which is a BARE identifier is
// actually bound in the flake being edited — as a per-system `let` binding or
// as an outputs argument. A dotted path (`pkgs.just`, `inputs.foo.bar`) is not
// checked: its root resolves through machinery this shallow parser does not
// model, and guessing there would produce false refusals. An empty New is a
// deletion and binds nothing.
//
// This is the guard that stops the destructive half of the fleet migration
// from writing a dangling reference when it runs before the additive half.
func checkBound(outs flakeparse.ParsedOutputs, replacement string) error {
	if replacement == "" || strings.Contains(replacement, ".") {
		return nil
	}

	if outs.LetExisting[replacement] || outs.ArgNames[replacement] {
		return nil
	}

	return fmt.Errorf(
		"%w: %q is neither a let binding nor an outputs argument — "+
			"run `conformist conform` first to add the binding, or the rewritten "+
			"flake will reference an undefined variable (nix-instantiate --parse "+
			"is syntax-only and will NOT catch it)",
		ErrUnboundElement, replacement,
	)
}

// listElementSplice locates Old as a complete token within ls.Inner and
// returns a Splice that replaces it with m.New (or removes its line when New
// is ""). ok is false when Old is not found as a token.
func listElementSplice(src []byte, ls flakeparse.ListSplice, m ListElementMigration) (flakeparse.Splice, bool) {
	idx := flakeparse.TokenIndex(ls.Inner, m.Old)
	if idx < 0 {
		return flakeparse.Splice{}, false
	}

	oldStart := ls.InnerStart() + idx
	oldEnd := oldStart + len(m.Old)

	if m.New == "" {
		// Delete the element's whole line when the line is otherwise blank.
		lineStart := flakeparse.LineStart(src, oldStart)
		lineEnd := oldEnd
		for lineEnd < len(src) && src[lineEnd] != '\n' {
			lineEnd++
		}
		if lineEnd < len(src) {
			lineEnd++
		}
		line := string(src[lineStart:lineEnd])
		if strings.TrimSpace(strings.ReplaceAll(line, m.Old, "")) == "" {
			return flakeparse.Splice{Offset: lineStart, End: lineEnd}, true
		}

		return flakeparse.Splice{Offset: oldStart, End: oldEnd}, true
	}

	return flakeparse.Splice{Offset: oldStart, End: oldEnd, Text: m.New}, true
}
