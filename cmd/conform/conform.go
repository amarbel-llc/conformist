// Package conform scaffolds a repo into the amarbel-llc conformist shape: it
// writes the files that are safe to create (conformist.nix, version.env) and
// reports the wiring a human must add to an existing flake.nix and justfile. It
// never edits existing files — auto-rewriting an arbitrary flake.nix is fragile,
// so conformist scaffolds the new pieces and prints the rest to paste.
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

// scaffold is one file conform may create.
type scaffold struct {
	name    string
	content []byte
}

// Run scaffolds the eng conformist shape into dir: it writes each scaffold file
// that is absent and skips those that already exist (idempotent), then prints the
// manual flake.nix + justfile wiring to out. It never edits an existing file.
func Run(dir string, out io.Writer) (Result, error) {
	var res Result

	for _, s := range []scaffold{
		{"conformist.nix", conformistNix},
		{"version.env", versionEnv},
	} {
		path := filepath.Join(dir, s.name)

		_, statErr := os.Stat(path)
		switch {
		case statErr == nil:
			res.Skipped = append(res.Skipped, s.name)

			continue
		case !os.IsNotExist(statErr):
			return res, fmt.Errorf("failed to stat %s: %w", s.name, statErr)
		}

		if err := os.WriteFile(path, s.content, 0o644); err != nil { //nolint:gosec // scaffold, not a secret
			return res, fmt.Errorf("failed to write %s: %w", s.name, err)
		}

		res.Wrote = append(res.Wrote, s.name)
	}

	writeReport(out, res)

	return res, nil
}

func writeReport(out io.Writer, res Result) {
	for _, name := range res.Wrote {
		fmt.Fprintf(out, "wrote %s\n", name)
	}

	for _, name := range res.Skipped {
		fmt.Fprintf(out, "kept  %s (already present)\n", name)
	}

	fmt.Fprintf(out, "\n# ---- add to flake.nix ----\n\n%s\n", flakeSnippet)
	fmt.Fprintf(out, "\n# ---- add to justfile ----\n\n%s\n", recipesJust)
	fmt.Fprint(out, "\nNext: `git add` the new files + flake.lock, then `just lint` "+
		"(or `nix build .#checks.<system>.formatting`).\n")
}
