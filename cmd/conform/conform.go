// Package conform scaffolds a repo into the amarbel-llc conformist shape. It
// writes every shape file that is absent — conformist.nix, version.env, sweatfile,
// and (for a greenfield repo) a complete flake.nix and justfile — and never edits
// a file that already exists. When flake.nix or justfile is already present,
// conform leaves it untouched and instead prints the wiring to paste, because
// auto-rewriting an arbitrary flake.nix is fragile.
package conform

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// Result reports what Run did, so a caller can pick an exit code (wrote anything
// => the tree changed).
type Result struct {
	Wrote   []string
	Skipped []string
}

// scaffold is one file conform may create. When the file already exists conform
// never edits it; if reportSnippet is non-empty (flake.nix, justfile), that
// paste-in wiring is printed instead, under the "add to <name>" header. Files
// with no snippet (conformist.nix, version.env) are simply skipped when present.
type scaffold struct {
	name          string
	content       []byte
	reportSnippet string
}

// scaffolds is the eng shape conform writes, in report order.
var scaffolds = []scaffold{
	{name: "conformist.nix", content: conformistNix},
	{name: "version.env", content: versionEnv},
	{name: "sweatfile", content: sweatfile},
	{name: "flake.nix", content: flakeNix, reportSnippet: flakeSnippet},
	{name: "justfile", content: justfile, reportSnippet: recipesJust},
}

// Run scaffolds the eng conformist shape into dir: it writes each shape file that
// is absent and skips those that already exist (idempotent). For an existing
// flake.nix / justfile it additionally prints the wiring to paste, since it never
// edits an existing file.
func Run(dir string, out io.Writer) (Result, error) {
	var (
		res     Result
		toPrint []scaffold // existing flake.nix/justfile whose snippet must be reported
	)

	for _, s := range scaffolds {
		path := filepath.Join(dir, s.name)

		_, statErr := os.Stat(path)
		switch {
		case statErr == nil:
			res.Skipped = append(res.Skipped, s.name)

			if s.reportSnippet != "" {
				toPrint = append(toPrint, s)
			}

			continue
		case !os.IsNotExist(statErr):
			return res, fmt.Errorf("failed to stat %s: %w", s.name, statErr)
		}

		if err := os.WriteFile(path, s.content, 0o644); err != nil { //nolint:gosec // scaffold, not a secret
			return res, fmt.Errorf("failed to write %s: %w", s.name, err)
		}

		res.Wrote = append(res.Wrote, s.name)
	}

	writeReport(out, res, toPrint)

	return res, nil
}

func writeReport(out io.Writer, res Result, toPrint []scaffold) {
	for _, name := range res.Wrote {
		fmt.Fprintf(out, "wrote %s\n", name)
	}

	for _, name := range res.Skipped {
		fmt.Fprintf(out, "kept  %s (already present)\n", name)
	}

	for _, s := range toPrint {
		fmt.Fprintf(out, "\n# ---- add to %s (already present, not edited) ----\n\n%s\n",
			s.name, s.reportSnippet)
	}

	fmt.Fprint(out, "\nNext: `git add` the new files + flake.lock, then `just lint` "+
		"(or `nix build .#checks.<system>.formatting`).\n")
}
