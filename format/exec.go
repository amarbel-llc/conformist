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

	"code.linenisgreat.com/conformist/rulejq"
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
	// One writer for both streams: the same *bytes.Buffer is recognized by
	// os/exec as a single destination, so the tool's stdout and stderr keep the
	// order it wrote them (what CombinedOutput gave). Every formatter and every
	// linter's check path relies on that fused, ordered output.
	var buf bytes.Buffer

	nonzero, err = inv.exec(ctx, dir, args, &buf, &buf)

	return nonzero, buf.String(), err
}

// runSplit is run with the two streams kept apart, for a caller that treats them
// differently: a repair-command's stderr is a complaint to surface, its stdout is
// narration to keep quiet (conformist#127). The streams are not ordered relative
// to each other; use run when the interleaving matters.
func (inv invocation) runSplit(
	ctx context.Context, dir string, args []string,
) (nonzero bool, stdout, stderr string, err error) {
	var out, errOut bytes.Buffer

	nonzero, err = inv.exec(ctx, dir, args, &out, &errOut)

	return nonzero, out.String(), errOut.String(), err
}

// exec runs the invocation writing the tool's stdout and stderr to the given
// writers. It is the one place a command is started, shared by run and runSplit.
func (inv invocation) exec(
	ctx context.Context, dir string, args []string, stdout, stderr io.Writer,
) (nonzero bool, err error) {
	if !inv.isShell() {
		cmd := exec.CommandContext(ctx, inv.exe, args...) //nolint:gosec
		cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
		cmd.Dir = dir
		cmd.Stdout = stdout
		cmd.Stderr = stderr

		if runErr := cmd.Run(); runErr != nil {
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				return true, nil
			}

			return false, fmt.Errorf("'%s' failed to execute: %w", inv.name, runErr)
		}

		return false, nil
	}

	// "--" terminates option parsing so args are taken as positional parameters
	// ($1, $2, …, "$@"), never misread as shell flags.
	runner, newErr := interp.New(
		interp.Dir(dir),
		interp.Env(inv.env),
		interp.StdIO(nil, stdout, stderr),
		interp.Params(append([]string{"--"}, args...)...),
		interp.ExecHandlers(ruleJQExecHandler),
	)
	if newErr != nil {
		return false, fmt.Errorf("'%s' failed to start: %w", inv.name, newErr)
	}

	if runErr := runner.Run(ctx, inv.program); runErr != nil {
		var status interp.ExitStatus
		if errors.As(runErr, &status) {
			return status != 0, nil
		}

		return false, fmt.Errorf("'%s' failed to execute: %w", inv.name, runErr)
	}

	return false, nil
}

// ruleJQExecHandler serves rulejq.Command in-process, so a profile rule
// pipeline runs its jq program without any jq on PATH (RFC 0005). Every other
// command falls through to normal execution.
func ruleJQExecHandler(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		if args[0] != rulejq.Command {
			return next(ctx, args)
		}

		hc := interp.HandlerCtx(ctx)

		if status := rulejq.Run(args[1:], hc.Dir, hc.Stdin, hc.Stdout, hc.Stderr); status != 0 {
			return interp.ExitStatus(status)
		}

		return nil
	}
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
