package rulejq_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/rulejq"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

func run(t *test_ui.T, program, input string) (uint8, string, string) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rule.jq"), []byte(program), 0o644))

	var stdout, stderr bytes.Buffer

	// A relative program path resolves against dir, as it would against a
	// linter's working directory.
	code := rulejq.Run([]string{"-f", "rule.jq"}, dir, strings.NewReader(input), &stdout, &stderr)

	return code, stdout.String(), stderr.String()
}

func TestRunRawStringsAndJSON(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	code, out, errOut := run(t,
		`.recipes[] | if .doc then "'\(.name)': \(.doc)" else {name, n: .n} end`,
		`{"recipes":[{"name":"build","doc":"it's fine"},{"name":"x","n":12345678901234567890}]}`)

	as.Equal(uint8(0), code, errOut)
	// Keys come out SORTED ("n" before "name"): gojq sorts object keys where jq
	// keeps insertion order. A rule must not depend on key order in its output.
	as.Equal("'build': it's fine\n{\n  \"n\": 12345678901234567890,\n  \"name\": \"x\"\n}\n", out,
		"strings print raw; other values as indented JSON; large integers keep their digits")
}

func TestRunStreamsEveryInputValue(tt *testing.T) {
	t := &test_ui.T{T: tt}

	code, out, errOut := run(t, `.a`, "{\"a\":\"one\"}\n{\"a\":\"two\"}\n")
	require.Equal(t, uint8(0), code, errOut)
	require.Equal(t, "one\ntwo\n", out)
}

func TestRunEmptyInputIsClean(tt *testing.T) {
	t := &test_ui.T{T: tt}

	code, out, _ := run(t, `error("never runs")`, "")
	require.Equal(t, uint8(0), code)
	require.Empty(t, out)
}

func TestRunFailures(tt *testing.T) {
	for name, tc := range map[string]struct {
		program, input string
		want           uint8
	}{
		"compile error":    {`.[`, `{}`, 3},
		"runtime error":    {`.a | tonumber`, `{"a":"x"}`, 5},
		"invalid input":    {`.`, `{not json`, 2},
		"undefined func":   {`nosuchfunction`, `{}`, 3},
		"explicit error()": {`error("boom")`, `{}`, 5},
	} {
		tt.Run(name, func(tt *testing.T) {
			t := &test_ui.T{T: tt}

			code, _, errOut := run(t, tc.program, tc.input)
			require.Equal(t, tc.want, code, errOut)
			require.Contains(t, errOut, rulejq.Command)
		})
	}
}

func TestRunRejectsOtherArguments(t *testing.T) {
	var stderr bytes.Buffer

	code := rulejq.Run([]string{".foo"}, t.TempDir(), strings.NewReader("{}"), &bytes.Buffer{}, &stderr)
	require.Equal(t, uint8(2), code)
	require.Contains(t, stderr.String(), "usage")
}
