// flakeclobber is a one-shot fleet-migration tool that applies targeted
// list-element replacements to flake.nix files. It is a separate binary
// from conformist: fleet sweeps are deliberate, not incidentally triggered
// by conform walking a repo.
//
// Usage:
//
//	flakeclobber [--old OLD --new NEW]... <file> [<file>...]
//
// Each --old/--new pair defines one substitution applied to every file.
// --new may be omitted (or "") to delete the matched element. Files that
// do not match the recognized eachDefaultSystem shape are skipped.
//
// Exit codes:
//
//	0 — all files processed (including skips); at least one file changed
//	    when --require-change is not set (default permissive)
//	1 — no files were changed (useful for --dry-run auditing)
//	2 — operational error (parse failure, I/O, bad flags)
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		os.Exit(2)
	}
}

func newRoot() *cobra.Command {
	var (
		olds []string
		news []string
	)

	cmd := &cobra.Command{
		Use:   "flakeclobber [--old OLD --new NEW]... <file>...",
		Short: "fleet-migration list-element substitutions in flake.nix files",
		Long: `flakeclobber applies targeted list-element replacements to flake.nix files.
Each --old/--new pair is one substitution applied to every listed file.
Files not matching the recognized eachDefaultSystem shape are skipped.`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return run(olds, news, args)
		},
	}

	cmd.Flags().StringArrayVar(&olds, "old", nil, "element to replace (repeatable)")
	cmd.Flags().StringArrayVar(&news, "new", nil, "replacement (parallel to --old; empty = delete)")

	return cmd
}

func run(olds, news []string, files []string) error {
	migrations, err := buildMigrations(olds, news)
	if err != nil {
		return err
	}

	var changed int

	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		out, report, err := Clobber(src, migrations)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		if report.Skipped != "" {
			fmt.Fprintf(os.Stderr, "skip %s: %s\n", path, report.Skipped)

			continue
		}

		if !report.Changed() {
			continue
		}

		err = os.WriteFile(path, out, 0o600) //nolint:gosec // G703: user-supplied paths
		if err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}

		for _, a := range report.Applied {
			fmt.Printf("%s: %s\n", path, a)
		}

		changed++
	}

	if changed == 0 {
		os.Exit(1)
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
		return nil, fmt.Errorf("more --new flags (%d) than --old flags (%d)", len(news), len(olds))
	}

	migrations := make([]ListElementMigration, len(olds))
	for i, old := range olds {
		migrations[i] = ListElementMigration{Old: old, New: news[i]}
	}

	return migrations, nil
}
