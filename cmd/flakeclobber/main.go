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
// --new may be omitted (or "") to delete the matched element.
//
// Exit codes:
//
//	0 — all files processed (changes applied, already migrated, or dry-run)
//	1 — one or more files could not be migrated (shape unrecognized,
//	    partial state, parse failure); non-failing files still processed
//	2 — operational error (bad flags, I/O failure)
package main

import (
	"bytes"
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
		RunE: func(_ *cobra.Command, args []string) error {
			return run(olds, news, apply, args)
		},
	}

	cmd.Flags().StringArrayVar(&olds, "old", nil, "element to replace (repeatable)")
	cmd.Flags().StringArrayVar(&news, "new", nil, "replacement (parallel to --old; empty = delete)")
	cmd.Flags().BoolVar(&apply, "apply", false, "write changes (default is dry-run)")

	return cmd
}

func run(olds, news []string, apply bool, files []string) error {
	migrations, err := buildMigrations(olds, news)
	if err != nil {
		return err
	}

	var failures int

	for _, path := range files {
		src, err := os.ReadFile(path) //nolint:gosec // G304: user-supplied path
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
			failures++
			continue
		}

		if !report.Changed() {
			// Already migrated (or N/A): print satisfied entries and continue.
			for _, s := range report.Satisfied {
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

		if err := nixParseCheck(out); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", path, err)
			failures++
			continue
		}

		if err = os.WriteFile(path, out, 0o600); err != nil { //nolint:gosec // G306
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	if failures > 0 {
		os.Exit(1)
	}

	return nil
}

// nixParseCheck pipes src through nix-instantiate --parse to verify the
// rewritten flake.nix is syntactically valid before writing it to disk.
func nixParseCheck(src []byte) error {
	cmd := exec.Command("nix-instantiate", "--parse", "/dev/stdin")
	cmd.Stdin = bytes.NewReader(src)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nix-instantiate --parse: %w", err)
	}
	return nil
}

func buildMigrations(olds, news []string) ([]ListElementMigration, error) {
	if len(olds) == 0 {
		return nil, errors.New("at least one --old flag is required")
	}
	// Pad news to match olds length with empty strings (= delete).
	for len(news) < len(olds) {
		news = append(news, "")
	}
	if len(news) > len(olds) {
		return nil, fmt.Errorf(
			"more --new flags (%d) than --old flags (%d)", len(news), len(olds),
		)
	}

	migrations := make([]ListElementMigration, len(olds))
	for i, old := range olds {
		migrations[i] = ListElementMigration{Old: old, New: news[i]}
	}

	return migrations, nil
}
