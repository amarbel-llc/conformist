// Package rulejq runs a jq program in-process with gojq, so the rule pipelines
// the RFC 0005 profile resolver generates never depend on a jq found on PATH.
// A consumer of the static profile route adds no flake inputs and may run in a
// bare environment (a spinclass pre-merge hook, a jq-less host); the jq that
// judges its rules therefore has to ship inside conformist itself.
//
// The command is deliberately NOT named `jq`: format's shell interpreter serves
// Command in-process, and a user's own `jq` in any other linter keeps meaning
// whatever jq that user's PATH provides.
package rulejq

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/itchyny/gojq"
)

// Command is the name a rule pipeline invokes; format's shell interpreter runs
// it in-process instead of looking it up on PATH.
const Command = "conformist-rule-jq"

// Exit codes follow jq's own: 2 for usage or unreadable input, 3 for a program
// that does not compile, 5 for a runtime error.
const (
	exitUsage   uint8 = 2
	exitCompile uint8 = 3
	exitRuntime uint8 = 5
)

// Run behaves as `jq -r -f FILE`: it reads a stream of JSON values from stdin,
// runs the program in FILE (resolved against dir when relative) over each, and
// writes strings raw and every other result as indented JSON. The only accepted
// arguments are `-f FILE`, the one form the resolver emits. It returns the exit
// status.
func Run(args []string, dir string, stdin io.Reader, stdout, stderr io.Writer) uint8 {
	fail := func(code uint8, err error) uint8 {
		fmt.Fprintf(stderr, "%s: %v\n", Command, err)

		return code
	}

	if len(args) != 2 || args[0] != "-f" {
		return fail(exitUsage, fmt.Errorf("usage: %s -f PROGRAM_FILE (got %q)", Command, args))
	}

	path := args[1]
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return fail(exitUsage, err)
	}

	query, err := gojq.Parse(string(src))
	if err != nil {
		return fail(exitCompile, fmt.Errorf("parsing %s: %w", path, err))
	}

	code, err := gojq.Compile(query)
	if err != nil {
		return fail(exitCompile, fmt.Errorf("compiling %s: %w", path, err))
	}

	if stdin == nil {
		stdin = strings.NewReader("")
	}

	decoder := json.NewDecoder(stdin)
	decoder.UseNumber()

	out := bufio.NewWriter(stdout)

	status := runStream(decoder, code, out)

	if err := out.Flush(); err != nil && status.err == nil {
		return fail(exitRuntime, err)
	}

	if status.err != nil {
		return fail(status.code, status.err)
	}

	return 0
}

type streamStatus struct {
	code uint8
	err  error
}

func runStream(decoder *json.Decoder, code *gojq.Code, out io.Writer) streamStatus {
	for {
		var input any

		if err := decoder.Decode(&input); err != nil {
			if errors.Is(err, io.EOF) {
				return streamStatus{}
			}

			return streamStatus{exitUsage, fmt.Errorf("reading input: %w", err)}
		}

		iter := code.Run(input)

		for {
			v, ok := iter.Next()
			if !ok {
				break
			}

			if err, isErr := v.(error); isErr {
				return streamStatus{exitRuntime, err}
			}

			if err := writeResult(out, v); err != nil {
				return streamStatus{exitRuntime, err}
			}
		}
	}
}

// writeResult prints one result the way `jq -r` does: a string raw, anything
// else as JSON indented by two spaces.
func writeResult(out io.Writer, v any) error {
	if s, ok := v.(string); ok {
		if _, err := io.WriteString(out, s+"\n"); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}

		return nil
	}

	encoded, err := gojq.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding result: %w", err)
	}

	var indented bytes.Buffer

	if err := json.Indent(&indented, encoded, "", "  "); err != nil {
		return fmt.Errorf("indenting result: %w", err)
	}

	indented.WriteByte('\n')

	if _, err := out.Write(indented.Bytes()); err != nil {
		return fmt.Errorf("writing result: %w", err)
	}

	return nil
}
