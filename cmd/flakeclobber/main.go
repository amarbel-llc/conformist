// flakeclobber applies targeted list-element replacements to flake.nix files
// as part of a fleet migration.
//
// By default flakeclobber runs in dry-run mode: it prints what it would
// change and exits 0 without writing any file. Pass --apply to write.
// When --apply is set, each rewritten file is verified via
// nix-instantiate --parse before being written to disk.
//
// Usage:
//
//	flakeclobber [--old OLD --new NEW]... [--apply] <file>...
//
// Each --old/--new pair defines one substitution applied to every file.
// --old and --new must be paired one-for-one; pass an explicit empty
// --new "" to DELETE the matched element.
//
// Writes are two-phase: every rewrite is computed and parse-gated before any
// file is written, so a splice regression cannot half-migrate a fleet. See
// run for the exact failure taxonomy and the residual non-atomicity.
//
// Exit codes:
//
//	0 — all files processed (changes applied, already migrated, N/A, or dry-run)
//	1 — one or more files could not be migrated (shape unrecognized,
//	    partial state, duplicate element); other files still processed.
//	    Also returned when a rewrite failed the nix parse gate — in which
//	    case NO file was written at all.
//	2 — operational error (bad flags, I/O failure)
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		os.Exit(2)
	}
}

func newRoot() *cobra.Command {
	var (
		olds  []string
		news  []string
		apply bool
	)

	cmd := &cobra.Command{
		Use:   "flakeclobber [--old OLD --new NEW]... [--apply] <file>...",
		Short: "fleet-migration list-element substitutions in flake.nix files",
		Long: `flakeclobber applies targeted list-element replacements to flake.nix files.

By default it is a dry-run: changes are printed but no file is written.
Pass --apply to write. Each --old/--new pair is one substitution.`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), olds, news, apply, args)
		},
	}

	cmd.Flags().StringArrayVar(&olds, "old", nil, "element to replace (repeatable)")
	cmd.Flags().StringArrayVar(&news, "new", nil,
		`replacement (one per --old, in order; pass "" to delete)`)
	cmd.Flags().BoolVar(&apply, "apply", false, "write changes (default is dry-run)")

	return cmd
}

// pendingWrite is a rewrite that passed the parse gate and is queued for
// phase 2.
type pendingWrite struct {
	path string
	out  []byte
}

// run is two-phase so a multi-file sweep cannot leave the fleet half-migrated.
//
// Phase 1 computes and parse-gates every rewrite, writing nothing. Phase 2
// writes the queued rewrites only if phase 1 produced no parse-gate failure.
// The two failure kinds are deliberately treated differently:
//
//   - A per-file MIGRATION refusal (unrecognized shape, no devShell, partial
//     state, duplicate element) is data, not a defect: those shapes are
//     expected across a heterogeneous fleet. It fails that file only and does
//     not block the others, and it never queues a write, so it cannot leave
//     partial state.
//   - A PARSE-GATE failure means the splice logic emitted invalid Nix. That
//     indicts the tool, so every other file's rewrite is equally suspect and
//     nothing is written at all.
//
// Residual non-atomicity: an I/O error partway through phase 2 leaves the
// files already written in their migrated state. Making that atomic would
// need a cross-file transaction; it is out of scope, so it is reported
// explicitly rather than papered over.
func run(ctx context.Context, olds, news []string, apply bool, files []string) error {
	migrations, err := buildMigrations(olds, news)
	if err != nil {
		return err
	}

	var (
		migrationFailures int
		parseFailures     int
		queued            []pendingWrite
	)

	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		out, report, clobberErr := Clobber(src, migrations)
		if clobberErr != nil {
			switch {
			case errors.Is(clobberErr, ErrUnrecognized):
				fmt.Fprintf(os.Stderr,
					"error: %s: not the recognized eachDefaultSystem shape\n", path)
			case errors.Is(clobberErr, ErrNoDevShell):
				fmt.Fprintf(os.Stderr,
					"error: %s: no devShells.default packages list found\n", path)
			default:
				fmt.Fprintf(os.Stderr, "error: %s: %v\n", path, clobberErr)
			}
			migrationFailures++

			continue
		}

		if !report.Changed() {
			// Already migrated, or the migration does not apply. Print BOTH
			// kinds of entry: a file that produced no output at all is
			// indistinguishable from a successful migration in a sweep log.
			for _, s := range report.Satisfied {
				fmt.Printf("%s: %s\n", path, s)
			}

			for _, s := range report.NotApplicable {
				fmt.Printf("%s: %s\n", path, s)
			}

			continue
		}

		// Changes to apply (or report in dry-run).
		prefix := ""
		if !apply {
			prefix = "[dry-run] "
		}
		for _, a := range report.Applied {
			fmt.Printf("%s: %s%s\n", path, prefix, a)
		}

		if !apply {
			continue
		}

		if err := nixParseCheck(ctx, out); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", path, err)
			parseFailures++

			continue
		}

		queued = append(queued, pendingWrite{path: path, out: out})
	}

	// Phase 2. A parse-gate failure aborts every write, including the
	// rewrites that individually passed.
	if parseFailures > 0 {
		fmt.Fprintf(os.Stderr,
			"error: %d file(s) failed the nix parse gate — no file was written\n",
			parseFailures)
	} else {
		for _, w := range queued {
			if err := os.WriteFile(w.path, w.out, 0o600); err != nil {
				return fmt.Errorf("write %s: %w", w.path, err)
			}
		}
	}

	if migrationFailures+parseFailures > 0 {
		os.Exit(1)
	}

	return nil
}

// nixParseCheck pipes src through nix-instantiate --parse to verify the
// rewritten flake.nix is syntactically valid before writing it to disk.
//
// The source argument MUST be "-" (read stdin), not "/dev/stdin": Go backs a
// non-*os.File cmd.Stdin with an os.Pipe, so nix resolves the /dev/stdin
// symlink to `/proc/<pid>/fd/pipe:[<inode>]` and rejects it with "path … does
// not exist" — making the gate fail unconditionally and silently reducing
// --apply to a no-op. Measured on nix 2.31.2: a path and
// `-` from a pipe both exit 0; /dev/stdin exits 0 only from a shell redirect
// and 1 from a pipe.
func nixParseCheck(ctx context.Context, src []byte) error {
	cmd := exec.CommandContext(ctx, "nix-instantiate", "--parse", "-")
	cmd.Stdin = bytes.NewReader(src)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nix-instantiate --parse: %w", err)
	}

	return nil
}

// buildMigrations pairs each --old with its --new. The counts must match
// EXACTLY: --new is never inferred. Padding a short --new list with empty
// strings would silently turn a missing or misspelled flag into a deletion —
// `--apply --old pkgs.just <file>` would remove the element rather than
// replace it. For a destructive tool that is an error, so deleting an element
// requires an explicit empty `--new ""`.
func buildMigrations(olds, news []string) ([]ListElementMigration, error) {
	if len(olds) == 0 {
		return nil, errors.New("at least one --old flag is required")
	}
	if len(news) != len(olds) {
		return nil, fmt.Errorf(
			"--old and --new must be paired: got %d --old and %d --new; "+
				`pass an explicit --new "" to delete an element`,
			len(olds), len(news),
		)
	}

	migrations := make([]ListElementMigration, len(olds))
	for i, old := range olds {
		migrations[i] = ListElementMigration{Old: old, New: news[i]}
	}

	return migrations, nil
}
