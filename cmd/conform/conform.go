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
//
// #42 (brownfield convergence): conform detects an existing flake.nix/justfile
// that already carries the conformist wiring and stays silent rather than
// re-printing the paste snippets (conformance detection); and it delegates the
// real content edits to conformist's own repair linters — the default report
// prints the single RepairCommand, and Options.Repair runs that same command
// over the working tree (no commit), so a conformed brownfield repo needs zero
// hand edits.
//
// #43 (greenfield-from-template): Bootstrap resolves a flake template advertised
// by a domain's PAPI document (cmd/conform/papi) and runs `nix flake init` from
// it. The no-arg local scaffold path (Run) is unchanged.
package conform

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// RepairCommand is the exact working-tree repair conform emits — and, with
// Options.Repair, runs — to bring an existing tree fully up to spec by
// delegating to conformist's own linters (#42): `nix fmt` for the pure formatter
// + file-linter repair, then the eng-impure lane's linters (agents-md,
// gomod2nix, …) in repair mode against the working tree. It makes NO commit — it
// leaves the changes staged/unstaged for the operator to review and commit. It
// is a single source of truth: the default report prints this string and
// --repair executes this same string, so the emitted and executed commands can
// never diverge (both greenfield-scaffolded and brownfield-edited flakes wire
// the `.#conformist-impure-config` output it references).
const RepairCommand = `nix fmt && conformist --config-file ` +
	`"$(nix build --no-link --print-out-paths .#conformist-impure-config)" --tree-root .`

// Options tunes a conform run.
type Options struct {
	// NoEdit disables in-place flake.nix editing: an existing flake.nix is
	// left untouched and its wiring is printed to paste instead (the
	// pre-in-place-editing behavior). It has no effect on a greenfield repo.
	NoEdit bool
	// ForceFormatter replaces an existing `formatter` attribute with
	// conformist's wrapper instead of reporting it as a conflict (#63).
	ForceFormatter bool
	// Repair runs RepairCommand inline after scaffolding, converging the tree
	// with zero further operator action (#42, the adoption-wave path). Without
	// it, conform only prints RepairCommand as the one command to run.
	Repair bool
	// RepairRunner runs the working-tree repair and reports whether it changed
	// the tree; nil uses defaultRepairRunner (sh -c over RepairCommand, with a
	// git-status delta). Tests inject a stub so no real nix invocation happens.
	RepairRunner func(dir, command string) (changed bool, err error)
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
	// Repaired reports whether an inline --repair run changed the tree (#42).
	Repaired bool
}

// Changed reports whether the run modified the tree.
func (r Result) Changed() bool { return len(r.Wrote) > 0 || len(r.Edited) > 0 || r.Repaired }

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
		edited, report, applyErr := editFlake(flakePath, opts)
		switch {
		case errors.Is(applyErr, flakeedit.ErrUnrecognized):
			res.Skipped = append(res.Skipped, "flake.nix")
			// #42 conformance detection: an unrecognized-shape flake that a user
			// already wired by hand (e.g. flake-parts) is conformant — stay
			// silent instead of nagging with the paste snippet.
			if flakeFileIsWired(flakePath) {
				flakeNote = "already conformant (wiring present)"
			} else {
				printFlake = true
				flakeNote = "unrecognized shape"
			}
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

	// justfile: write when absent; otherwise print the recipes (never edited),
	// unless it already carries conformist's lint recipes (#42 conformance
	// detection — stay silent for a conformed repo).
	wroteJust, err := writeIfAbsent(dir, "justfile", justfile)
	if err != nil {
		return res, err
	}
	if wroteJust {
		res.Wrote = append(res.Wrote, "justfile")
	} else {
		res.Skipped = append(res.Skipped, "justfile")
		if !justfileIsWired(dir) {
			printJust = true
		}
	}

	// #42(ii): with --repair, delegate the real edits to conformist's own
	// linters by running RepairCommand over the working tree; otherwise the
	// report prints that one command for the operator to run.
	if opts.Repair {
		if err := runRepair(dir, opts, &res, out); err != nil {
			return res, err
		}
	}

	// opts.Repair implies runRepair already ran above, so pass it as "did we
	// run repair" — when true the report skips the emit-the-command hint.
	writeReport(out, res, printFlake, flakeNote, printJust, opts.Repair)

	return res, nil
}

// runRepair executes RepairCommand over dir's working tree and records whether
// it changed anything. It is only called with Options.Repair set (#42).
func runRepair(dir string, opts Options, res *Result, out io.Writer) error {
	runner := opts.RepairRunner
	if runner == nil {
		runner = defaultRepairRunner
	}

	changed, err := runner(dir, RepairCommand)
	if err != nil {
		return fmt.Errorf("repair failed (%s): %w", RepairCommand, err)
	}

	res.Repaired = changed
	if changed {
		fmt.Fprintln(out, "repair: applied working-tree fixes (review and commit them)")
	} else {
		fmt.Fprintln(out, "repair: working tree already conformant")
	}

	return nil
}

// editFlake reads flake.nix and runs the in-place editor over it.
func editFlake(path string, opts Options) ([]byte, flakeedit.EditReport, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, flakeedit.EditReport{}, fmt.Errorf("failed to read flake.nix: %w", err)
	}

	out, report, err := flakeedit.Apply(src, flakeedit.Options{ForceFormatter: opts.ForceFormatter})
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

func writeReport(out io.Writer, res Result, printFlake bool, flakeNote string, printJust, ranRepair bool) {
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

	fmt.Fprint(out, "\nNext: `git add` the new/edited files + flake.lock.\n")

	// #42(ii): unless we just ran it (--repair), print the ONE command that
	// brings the working tree fully up to spec by delegating to conformist's own
	// linters. This is exactly what --repair runs (RepairCommand), so the
	// emitted and executed forms are one and the same.
	if !ranRepair {
		fmt.Fprintf(out, "To converge fully (format + eng-lint repair), run inside the repo's devShell:\n  %s\n"+
			"or re-run `conform --repair` to run it for you.\n", RepairCommand)
	}
}

// justfileIsWired reports whether an existing justfile already carries
// conformist's lint recipes, so conform stays silent instead of re-printing the
// paste snippet (#42 conformance detection). The `checks.${system}.formatting`
// build target and a `lint-fmt` recipe are both conformist-specific markers.
func justfileIsWired(dir string) bool {
	content, err := os.ReadFile(filepath.Join(dir, "justfile"))
	if err != nil {
		return false
	}
	s := string(content)

	return strings.Contains(s, "checks.${system}.formatting") || reLintFmtRecipe.MatchString(s)
}

// reLintFmtRecipe matches a `lint-fmt` just recipe definition at column 0.
var reLintFmtRecipe = regexp.MustCompile(`(?m)^lint-fmt\s*:`)

// flakeFileIsWired reports whether the flake.nix at path already references
// conformist's module wiring, so an unrecognized-shape-but-hand-wired flake is
// treated as conformant (#42 conformance detection).
func flakeFileIsWired(path string) bool {
	src, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(src)

	return strings.Contains(s, "conformist.lib.evalModule") ||
		(strings.Contains(s, "conformist.url") && strings.Contains(s, "eval.config.build."))
}

// defaultRepairRunner runs RepairCommand via `sh -c` in dir and reports whether
// the working tree changed, using a git-status delta taken around the run. It
// inherits stderr so formatter/linter output and nix's flake-trust prompts reach
// the operator, but keeps conform's own stdout clean.
func defaultRepairRunner(dir, command string) (bool, error) {
	before, err := gitStatusPorcelain(dir)
	if err != nil {
		return false, err
	}

	cmd := exec.CommandContext(context.Background(), "sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("running repair command: %w", err)
	}

	after, err := gitStatusPorcelain(dir)
	if err != nil {
		return false, err
	}

	return before != after, nil
}

// gitStatusPorcelain returns `git status --porcelain --untracked-files=all` for
// dir, used to detect whether the repair run changed the tree. An error (e.g.
// not a git worktree) is surfaced so the caller fails loudly rather than
// silently reporting "no change".
func gitStatusPorcelain(dir string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", "-C", dir,
		"status", "--porcelain", "--untracked-files=all")

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git status in %s (repair needs a git worktree): %w", dir, err)
	}

	return string(out), nil
}
