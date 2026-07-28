package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	flakeparse "code.linenisgreat.com/conformist/cmd/conform/flakeparse"
)

// ListElementMigration describes one list-element substitution within the
// per-system return attrset or devShells packages list.
type ListElementMigration struct {
	// Old is the exact element text to search for (e.g. "pkgs.just").
	Old string
	// New is the replacement text (e.g. "justPkg"). Empty New deletes the
	// element. If Old is not found, the migration is a no-op for this file.
	New string
}

// ClobberReport summarizes what Clobber changed for one file.
type ClobberReport struct {
	// Applied lists each migration that matched and was applied.
	Applied []string
	// Skipped is non-empty when the file was not the recognized shape.
	Skipped string
}

// Changed reports whether the file was modified.
func (r ClobberReport) Changed() bool { return len(r.Applied) > 0 }

// Clobber applies migrations to src and returns the rewritten source plus a
// report. The recognized shape is flakeparse's eachDefaultSystem. Files
// not matching the shape are returned unchanged with Skipped set.
//
// Each ListElementMigration is applied to the devShells packages list
// located by flakeparse, if one is found. Only exact-token matches are
// replaced: the element must appear as a complete whitespace/bracket-
// delimited token, not as a substring of another identifier.
func Clobber(src []byte, migrations []ListElementMigration) ([]byte, ClobberReport, error) {
	_, outs, err := flakeparse.ParseFlake(src)
	if err != nil {
		if errors.Is(err, flakeparse.ErrUnrecognized) {
			return src, ClobberReport{Skipped: "not the recognized eachDefaultSystem shape"}, nil
		}

		return src, ClobberReport{}, fmt.Errorf("flakeclobber: parse: %w", err)
	}

	if outs.DevShellPackages == nil {
		return src, ClobberReport{Skipped: "no devShells.default packages list found"}, nil
	}

	ls := *outs.DevShellPackages

	var (
		report  ClobberReport
		splices []flakeparse.Splice
	)

	for _, m := range migrations {
		sp, ok := listElementSplice(src, ls, m)
		if !ok {
			continue
		}

		splices = append(splices, sp)

		if m.New == "" {
			report.Applied = append(report.Applied, fmt.Sprintf("removed %q", m.Old))
		} else {
			report.Applied = append(report.Applied, fmt.Sprintf("replaced %q with %q", m.Old, m.New))
		}
	}

	if len(splices) == 0 {
		return src, report, nil
	}

	// Apply highest offset first so earlier offsets stay valid.
	out := src
	for _, s := range slices.Backward(splices) {
		out = s.ApplyTo(out)
	}

	return out, report, nil
}

// listElementSplice locates Old as a complete token within ls.Inner and
// returns a Splice that replaces it with m.New (or removes it when New
// is ""). ok is false when Old is not found as a token.
func listElementSplice(src []byte, ls flakeparse.ListSplice, m ListElementMigration) (flakeparse.Splice, bool) {
	inner := ls.Inner

	// Find Old as a complete token: surrounded by non-identifier characters.
	idx := tokenIndex(inner, m.Old)
	if idx < 0 {
		return flakeparse.Splice{}, false
	}

	// The absolute start of inner in src is ls.CloseOff - (len(inner)-1).
	// inner[0] is the opening '[' which is at (ls.CloseOff - len(inner) + 1).
	// Wait — ls.Inner includes the brackets: inner[0]='[', inner[len-1]=']'.
	// ls.CloseOff is the offset of the ']'. So inner[0] is at
	// ls.CloseOff - len(inner) + 1.
	innerStart := ls.CloseOff - len(inner) + 1

	oldStart := innerStart + idx
	oldEnd := oldStart + len(m.Old)

	if m.New == "" {
		// Deletion: remove the element plus surrounding whitespace on its
		// line, leaving the line blank (the caller can run a formatter after).
		lineStart := flakeparse.LineStart(src, oldStart)
		// Extend to end of line (including newline).
		lineEnd := oldEnd
		for lineEnd < len(src) && src[lineEnd] != '\n' {
			lineEnd++
		}
		if lineEnd < len(src) {
			lineEnd++ // consume the newline
		}
		// Only delete the whole line if it is otherwise blank.
		line := string(src[lineStart:lineEnd])
		if strings.TrimSpace(strings.ReplaceAll(line, m.Old, "")) == "" {
			return flakeparse.Splice{Offset: lineStart, End: lineEnd}, true
		}
		// Otherwise replace just the token.
		return flakeparse.Splice{Offset: oldStart, End: oldEnd, Text: ""}, true
	}

	return flakeparse.Splice{Offset: oldStart, End: oldEnd, Text: m.New}, true
}

// tokenIndex returns the byte offset of needle within s when needle
// appears as a complete identifier token (bounded by non-identifier
// characters or string edges). Returns -1 when not found.
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

// isIdentChar reports whether r can appear inside a Nix identifier.
func isIdentChar(r rune) bool {
	return r == '_' || r == '-' || r == '\'' || r == '.' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}
