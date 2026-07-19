package conform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"code.linenisgreat.com/conformist/cmd/conform/papi"
)

// ErrTargetNotEmpty means Bootstrap was asked to scaffold into a directory that
// already holds files (other than .git); pass Overwrite to proceed.
var ErrTargetNotEmpty = errors.New("target directory is not empty; pass --overwrite to scaffold into it anyway")

// BootstrapOptions tunes a `conform <domain>` bootstrap run.
type BootstrapOptions struct {
	// Overwrite allows `nix flake init` to scaffold into a non-empty directory.
	Overwrite bool
	// Interactive enables the huh chooser for an ambiguous bare-domain selection
	// (more than one visible template). When false — no TTY — an ambiguous
	// selection fails with the id list instead of guessing (§8.2).
	Interactive bool
	// Resolver performs PAPI resolution; the zero value uses http.DefaultClient.
	// Tests inject a fixture-backed client.
	Resolver papi.Resolver
	// FlakeInit runs `nix flake init -t <flakeref>` in dir; nil uses runFlakeInit.
	// Tests substitute a stub so no real nix invocation happens.
	FlakeInit func(ctx context.Context, dir, flakeref string) error
}

// Bootstrap resolves a flake template advertised by target's domain PAPI and
// initializes dir from it. target is `<domain>` or `<domain>#<id>`. It surfaces
// the resolved flakeref before running `nix flake init` and refuses to scaffold
// over a non-empty dir unless opts.Overwrite. The no-arg local scaffold path
// (Run) is unaffected.
func Bootstrap(ctx context.Context, target, dir string, out io.Writer, opts BootstrapOptions) error {
	domain, id := papi.SplitTarget(target)
	if domain == "" {
		return errors.New("no domain given for template bootstrap")
	}

	templates, err := opts.Resolver.Resolve(ctx, domain)
	if err != nil {
		return fmt.Errorf("resolving templates for %s: %w", domain, err)
	}

	var chooser papi.Chooser
	if opts.Interactive {
		chooser = promptTemplate
	}

	tmpl, err := papi.Select(templates, id, chooser)
	if err != nil {
		return fmt.Errorf("selecting template from %s: %w", domain, err)
	}

	// Security: surface the resolved flakeref BEFORE init so the operator sees
	// exactly what will be instantiated (and can abort at nix's flake-trust
	// prompt, which runFlakeInit deliberately does not suppress).
	fmt.Fprintf(out, "resolved %s#%s -> %s\n", domain, tmpl.ID, tmpl.Flakeref)
	if tmpl.Description != "" {
		fmt.Fprintf(out, "  %s\n", tmpl.Description)
	}

	if !opts.Overwrite {
		empty, err := dirIsEmpty(dir)
		if err != nil {
			return err
		}
		if !empty {
			return fmt.Errorf("%w: %s", ErrTargetNotEmpty, dir)
		}
	}

	flakeInit := opts.FlakeInit
	if flakeInit == nil {
		flakeInit = runFlakeInit
	}

	if err := flakeInit(ctx, dir, tmpl.Flakeref); err != nil {
		return fmt.Errorf("nix flake init from %s: %w", tmpl.Flakeref, err)
	}

	fmt.Fprintf(out, "initialized %s from %s\n", dir, tmpl.Flakeref)
	fmt.Fprint(out, "\nNext: `git add` the scaffold, `nix flake lock`, then `conform` to finish wiring "+
		"(or `conform --repair` to converge fully).\n")

	return nil
}

// dirIsEmpty reports whether dir holds no entries other than a `.git` directory,
// so a freshly `git init`ed repo still counts as empty for bootstrap purposes. A
// missing dir counts as empty (nix flake init creates it).
func dirIsEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading target dir %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}

		return false, nil
	}

	return true, nil
}

// runFlakeInit runs `nix flake init -t <flakeref>` in dir, inheriting stdio so
// nix's own flake-trust prompts reach the operator (the spec forbids suppressing
// them). nix's stdout is routed to stderr so conform's own stdout stays clean.
func runFlakeInit(ctx context.Context, dir, flakeref string) error {
	cmd := exec.CommandContext(ctx, "nix", "flake", "init", "-t", flakeref)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	return cmd.Run() //nolint:wrapcheck // Bootstrap wraps with the flakeref context
}
