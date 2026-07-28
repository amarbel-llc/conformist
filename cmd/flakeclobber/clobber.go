package main

import (
	"errors"
	"fmt"
	"slices"
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
}

// Changed reports whether Clobber produced a rewritten source.
func (r ClobberReport) Changed() bool { return len(r.Applied) > 0 }

// elementStatus categorizes a migration entry relative to the current list.
type elementStatus int

const (
	elementPending   elementStatus = iota // Old present, New absent → needs migration
	elementSatisfied                      // Old absent, New present (or deleted) → done
	elementUnknown                        // neither Old nor New found → unexpected state
	elementConflict                       // both Old and New found → ambiguous
)

// checkElement determines the status of one migration entry within inner.
func checkElement(inner string, m ListElementMigration) elementStatus {
	hasOld := tokenIndex(inner, m.Old) >= 0
	if m.New == "" {
		// Deletion: satisfied when Old is absent.
		if hasOld {
			return elementPending
		}

		return elementSatisfied
	}
	hasNew := tokenIndex(inner, m.New) >= 0
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
		return src, ClobberReport{}, err // caller distinguishes ErrUnrecognized
	}

	if outs.DevShellPackages == nil {
		return src, ClobberReport{}, ErrNoDevShell
	}

	ls := *outs.DevShellPackages

	// Categorize each migration entry.
	type entry struct {
		m      ListElementMigration
		status elementStatus
	}
	entries := make([]entry, len(migrations))
	for i, m := range migrations {
		entries[i] = entry{m: m, status: checkElement(ls.Inner, m)}
	}

	var pending, satisfied int
	for _, e := range entries {
		switch e.status {
		case elementPending:
			pending++
		case elementSatisfied:
			satisfied++
		case elementUnknown:
			// N/A: neither Old nor New found; migration doesn't apply to this
			// file. Do not count toward partial-state detection.
		case elementConflict:
			return src, ClobberReport{}, fmt.Errorf(
				"flakeclobber: both %q and %q present in packages list — ambiguous state",
				e.m.Old, e.m.New,
			)
		}
	}

	// All satisfied/N/A → idempotent no-op.
	if pending == 0 {
		report := ClobberReport{}
		for _, e := range entries {
			if e.status != elementSatisfied {
				continue // skip N/A entries
			}
			if e.m.New != "" {
				report.Satisfied = append(
					report.Satisfied,
					fmt.Sprintf("%q already replaced with %q", e.m.Old, e.m.New),
				)
			} else {
				report.Satisfied = append(report.Satisfied, fmt.Sprintf("%q already removed", e.m.Old))
			}
		}

		return src, report, nil
	}

	// Mixed satisfied + pending → partial state, refuse all edits.
	if satisfied > 0 {
		return src, ClobberReport{}, ErrPartialState
	}

	// All pending → build splices.
	var (
		report  ClobberReport
		splices []flakeparse.Splice
	)

	for _, e := range entries {
		if e.status != elementPending {
			continue
		}

		sp, ok := listElementSplice(src, ls, e.m)
		if !ok {
			return src, ClobberReport{}, fmt.Errorf("flakeclobber: could not locate span for %q", e.m.Old)
		}

		splices = append(splices, sp)

		if e.m.New == "" {
			report.Applied = append(report.Applied, fmt.Sprintf("removed %q", e.m.Old))
		} else {
			report.Applied = append(report.Applied, fmt.Sprintf("replaced %q with %q", e.m.Old, e.m.New))
		}
	}

	out := src
	for _, s := range slices.Backward(splices) {
		out = s.ApplyTo(out)
	}

	return out, report, nil
}

// listElementSplice locates Old as a complete token within ls.Inner and
// returns a Splice that replaces it with m.New (or removes its line when New
// is ""). ok is false when Old is not found as a token.
func listElementSplice(src []byte, ls flakeparse.ListSplice, m ListElementMigration) (flakeparse.Splice, bool) {
	inner := ls.Inner

	idx := tokenIndex(inner, m.Old)
	if idx < 0 {
		return flakeparse.Splice{}, false
	}

	// inner[0] is the '[' at absolute offset (ls.CloseOff - len(inner) + 1).
	innerStart := ls.CloseOff - len(inner) + 1

	oldStart := innerStart + idx
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

// tokenIndex returns the byte offset of needle within s when needle appears
// as a complete identifier token. Returns -1 when not found.
func tokenIndex(s, needle string) int {
	for i := 0; i <= len(s)-len(needle); i++ {
		if s[i:i+len(needle)] != needle {
			continue
		}
		if i > 0 && isIdentChar(rune(s[i-1])) {
			continue
		}
		end := i + len(needle)
		if end < len(s) && isIdentChar(rune(s[end])) {
			continue
		}

		return i
	}

	return -1
}

// isIdentChar reports whether r can appear inside a Nix identifier or dotted
// attr-path.
func isIdentChar(r rune) bool {
	return r == '_' || r == '-' || r == '\'' || r == '.' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}
