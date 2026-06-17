package format

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// invocation is a resolved tool command (conformist#38). It is either a single
// bare executable — resolved from PATH and exec'd directly, exactly as before
// #38 — or a shell line run through the in-process shell interpreter
// (mvdan.cc/sh). A shell line lets a tool cd into a subdirectory, chain steps
// with `&&`, or pipe; a bare command keeps the original fast path, errors, and
// cache signature.
type invocation struct {
	name    string         // tool name, for diagnostics
	raw     string         // the original command string
	exe     string         // resolved executable; empty => shell line
	program *syntax.File   // parsed shell program; nil => bare executable
	env     expand.Environ // environment for PATH resolution / the interpreter
}

// newInvocation parses command and resolves it. A single literal word is a bare
// executable looked up on PATH (unchanged from the pre-#38 behavior, including
// the not-found error); anything else — multiple words, operators (`&&`, `|`),
// redirections, expansions — is a shell line run via the interpreter.
func newInvocation(name, treeRoot string, env expand.Environ, command string) (invocation, error) {
	program, err := syntax.NewParser().Parse(strings.NewReader(command), name)
	if err != nil {
		return invocation{}, fmt.Errorf("failed to parse command %q for '%s': %w", command, name, err)
	}

	inv := invocation{name: name, raw: command, env: env}

	if word, ok := singleLiteralWord(program); ok {
		exe, lookErr := interp.LookPathDir(treeRoot, env, word)
		if lookErr != nil {
			return invocation{}, fmt.Errorf("looking up %q: %w", word, lookErr)
		}

		inv.exe = exe

		return inv, nil
	}

	inv.program = program

	return inv, nil
}

// isShell reports whether the invocation is a shell line (vs a bare executable).
func (inv invocation) isShell() bool { return inv.program != nil }

// singleLiteralWord returns the lone literal word of a command that is a single
// simple command with exactly one argument and no assignments, redirections,
// negation, or backgrounding — i.e. a bare `nixfmt`-style executable name. ok is
// false for anything that needs the shell interpreter.
func singleLiteralWord(f *syntax.File) (string, bool) {
	if len(f.Stmts) != 1 {
		return "", false
	}

	stmt := f.Stmts[0]
	if stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown || len(stmt.Redirs) > 0 {
		return "", false
	}

	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 || len(call.Args) != 1 {
		return "", false
	}

	parts := call.Args[0].Parts
	if len(parts) != 1 {
		return "", false
	}

	lit, ok := parts[0].(*syntax.Lit)
	if !ok {
		return "", false
	}

	return lit.Value, true
}

// run executes the invocation in dir with args appended: a bare command gets
// them as argv after the executable; a shell line gets them as positional
// parameters, so the line references "$@" for the file list. It returns
// nonzero=true when the command exited non-zero (findings, for a linter) and a
// non-nil err only for an operational failure (failure to exec/parse).
func (inv invocation) run(ctx context.Context, dir string, args []string) (nonzero bool, output string, err error) {
	if !inv.isShell() {
		cmd := exec.CommandContext(ctx, inv.exe, args...) //nolint:gosec
		cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
		cmd.Dir = dir

		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				return true, string(out), nil
			}

			return false, string(out), fmt.Errorf("'%s' failed to execute: %w", inv.name, runErr)
		}

		return false, string(out), nil
	}

	var buf bytes.Buffer

	// "--" terminates option parsing so args are taken as positional parameters
	// ($1, $2, …, "$@"), never misread as shell flags.
	runner, newErr := interp.New(
		interp.Dir(dir),
		interp.Env(inv.env),
		interp.StdIO(nil, &buf, &buf),
		interp.Params(append([]string{"--"}, args...)...),
	)
	if newErr != nil {
		return false, "", fmt.Errorf("'%s' failed to start: %w", inv.name, newErr)
	}

	if runErr := runner.Run(ctx, inv.program); runErr != nil {
		var status interp.ExitStatus
		if errors.As(runErr, &status) {
			return status != 0, buf.String(), nil
		}

		return false, buf.String(), fmt.Errorf("'%s' failed to execute: %w", inv.name, runErr)
	}

	return false, buf.String(), nil
}

// signature contributes the invocation's identity to a cache hash h. A bare
// executable stats the binary, so a tool upgrade invalidates the cache (as
// before #38). A shell line has no single binary to stat, so it hashes the
// command string: the cache invalidates when the line changes, but not when an
// underlying binary the line calls is upgraded.
func (inv invocation) signature(h io.Writer) error {
	if inv.isShell() {
		_, _ = io.WriteString(h, inv.raw)

		return nil
	}

	info, err := os.Lstat(inv.exe)
	if err != nil {
		return fmt.Errorf("failed to stat executable: %w", err)
	}

	_, _ = h.Write(fmt.Appendf(nil, "%d %d", info.Size(), info.ModTime().Unix()))

	return nil
}
