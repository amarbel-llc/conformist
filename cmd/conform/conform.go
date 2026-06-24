// Package conform scaffolds a repo into the amarbel-llc conformist shape. It
// writes every shape file that is absent — conformist.nix, version.env, sweatfile,
// and (for a greenfield repo) a complete flake.nix and justfile. version.env's
// key is derived from the repo name (git origin remote, else the directory).
//
// For an existing flake.nix it edits it IN PLACE — splicing the conformist input
// and the per-system outputs wiring — when the file is the recognized
// eachDefaultSystem shape; any other shape (or --no-edit) falls back to printing
// the wiring to paste. An existing justfile is never edited; its recipes are
// printed.
package conform

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/amarbel-llc/conformist/cmd/conform/flakeedit"
	"github.com/amarbel-llc/conformist/git"
)

//go:embed scaffold/conformist.nix
var conformistNix []byte

//go:embed scaffold/version.env
var versionEnv []byte

//go:embed scaffold/flake.nix
var flakeNix []byte

//go:embed scaffold/justfile
var justfile []byte

//go:embed scaffold/sweatfile
var sweatfile []byte

//go:embed scaffold/flake-snippet.txt
var flakeSnippet string

//go:embed scaffold/recipes.just
var recipesJust string

// Options tunes a conform run.
type Options struct {
	// NoEdit disables in-place flake.nix editing: an existing flake.nix is
	// left untouched and its wiring is printed to paste instead (the
	// pre-in-place-editing behavior). It has no effect on a greenfield repo.
	NoEdit bool
}

// Result reports what Run did, so a caller can pick an exit code (anything
// written or edited => the tree changed).
type Result struct {
	// Wrote lists files created from scratch.
	Wrote []string
	// Edited lists the pieces spliced into an existing flake.nix in place.
	Edited []string
	// Conflicts lists output attributes an existing flake.nix already
	// defined differently, left untouched for the user to reconcile.
	Conflicts []string
	// Skipped lists existing files left as-is (already present / conformant).
	Skipped []string
}

// Changed reports whether the run modified the tree.
func (r Result) Changed() bool { return len(r.Wrote) > 0 || len(r.Edited) > 0 }

// Run scaffolds the eng conformist shape into dir. It writes each absent shape
// file, edits an existing flake.nix in place (unless opts.NoEdit), and prints
// wiring for anything it could not apply. It is idempotent.
func Run(dir string, out io.Writer, opts Options) (Result, error) {
	var (
		res        Result
		printFlake bool // print the flake.nix wiring snippet
		printJust  bool // print the justfile recipes snippet
		flakeNote  string
	)

	// Plain write-if-absent files (no in-place editing).
	plain := []struct {
		name    string
		content []byte
	}{
		{"conformist.nix", conformistNix},
		{"version.env", renderVersionEnv(dir)},
		{"sweatfile", sweatfile},
	}
	for _, p := range plain {
		wrote, err := writeIfAbsent(dir, p.name, p.content)
		if err != nil {
			return res, err
		}
		if wrote {
			res.Wrote = append(res.Wrote, p.name)
		} else {
			res.Skipped = append(res.Skipped, p.name)
		}
	}

	// flake.nix: write when absent; otherwise edit in place (or print).
	flakePath := filepath.Join(dir, "flake.nix")
	exists, err := fileExists(flakePath)
	if err != nil {
		return res, err
	}
	switch {
	case !exists:
		if err := os.WriteFile(flakePath, flakeNix, 0o644); err != nil { //nolint:gosec // scaffold, not a secret
			return res, fmt.Errorf("failed to write flake.nix: %w", err)
		}
		res.Wrote = append(res.Wrote, "flake.nix")
	case opts.NoEdit:
		res.Skipped = append(res.Skipped, "flake.nix")
		printFlake = true
		flakeNote = "--no-edit"
	default:
		edited, report, applyErr := editFlake(flakePath)
		switch {
		case errors.Is(applyErr, flakeedit.ErrUnrecognized):
			res.Skipped = append(res.Skipped, "flake.nix")
			printFlake = true
			flakeNote = "unrecognized shape"
		case applyErr != nil:
			return res, applyErr
		case report.Changed():
			if err := os.WriteFile(flakePath, edited, 0o644); err != nil { //nolint:gosec // scaffold, not a secret
				return res, fmt.Errorf("failed to write flake.nix: %w", err)
			}
			res.Edited = report.Added
			res.Conflicts = report.Conflicts
		default:
			res.Skipped = append(res.Skipped, "flake.nix")
			flakeNote = "already conformant"
		}
	}

	// justfile: write when absent; otherwise print the recipes (never edited).
	wroteJust, err := writeIfAbsent(dir, "justfile", justfile)
	if err != nil {
		return res, err
	}
	if wroteJust {
		res.Wrote = append(res.Wrote, "justfile")
	} else {
		res.Skipped = append(res.Skipped, "justfile")
		printJust = true
	}

	writeReport(out, res, printFlake, flakeNote, printJust)

	return res, nil
}

// editFlake reads flake.nix and runs the in-place editor over it.
func editFlake(path string) ([]byte, flakeedit.EditReport, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, flakeedit.EditReport{}, fmt.Errorf("failed to read flake.nix: %w", err)
	}

	out, report, err := flakeedit.Apply(src)
	if err != nil {
		return nil, report, fmt.Errorf("editing flake.nix: %w", err)
	}

	return out, report, nil
}

// writeIfAbsent writes content to dir/name when the file does not exist,
// returning whether it wrote.
func writeIfAbsent(dir, name string, content []byte) (bool, error) {
	path := filepath.Join(dir, name)
	exists, err := fileExists(path)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if err := os.WriteFile(path, content, 0o644); err != nil { //nolint:gosec // scaffold, not a secret
		return false, fmt.Errorf("failed to write %s: %w", name, err)
	}

	return true, nil
}

// fileExists reports whether path exists, distinguishing a real stat error.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, fmt.Errorf("failed to stat %s: %w", filepath.Base(path), err)
	}
}

// renderVersionEnv fills the version.env scaffold's key with the repo's name.
func renderVersionEnv(dir string) []byte {
	key := projectVersionKey(dir)

	return []byte(strings.ReplaceAll(string(versionEnv), "EXAMPLE_VERSION", key+"_VERSION"))
}

// projectVersionKey derives the version.env key prefix from the repo: the git
// origin remote's repo name, falling back to the directory basename, normalized
// to upper-snake-case.
func projectVersionKey(dir string) string {
	name, err := git.OriginRepoName(dir)
	if err != nil || name == "" {
		name = filepath.Base(dir)
	}

	return normalizeVersionKey(name)
}

// normalizeVersionKey upper-cases name and replaces every non-alphanumeric rune
// with '_', matching the eng-versioning key shape (<NAME>_VERSION).
func normalizeVersionKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}

	return b.String()
}

func writeReport(out io.Writer, res Result, printFlake bool, flakeNote string, printJust bool) {
	for _, name := range res.Wrote {
		fmt.Fprintf(out, "wrote %s\n", name)
	}

	if len(res.Edited) > 0 {
		fmt.Fprintf(out, "edited flake.nix (added: %s)\n", strings.Join(res.Edited, ", "))
	}
	for _, c := range res.Conflicts {
		fmt.Fprintf(out, "kept  flake.nix %s (already defined; reconcile by hand)\n", c)
	}

	for _, name := range res.Skipped {
		note := "already present"
		if name == "flake.nix" && flakeNote != "" {
			note = flakeNote
		}
		fmt.Fprintf(out, "kept  %s (%s)\n", name, note)
	}

	if printFlake {
		fmt.Fprintf(out, "\n# ---- add to flake.nix (%s) ----\n\n%s\n", flakeNote, flakeSnippet)
	}
	if printJust {
		fmt.Fprintf(out, "\n# ---- add to justfile (already present, not edited) ----\n\n%s\n", recipesJust)
	}

	fmt.Fprint(out, "\nNext: `git add` the new/edited files + flake.lock, then `just lint` "+
		"(or `nix build .#checks.<system>.formatting`).\n")
}
