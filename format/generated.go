package format

import (
	"bufio"
	"os"
	"regexp"
	"strings"

	"code.linenisgreat.com/conformist/walk"
	"github.com/charmbracelet/log"
)

// generatedMarker is the standard generated-code marker line
// (https://golang.org/s/generatedcode).
var generatedMarker = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// maxHeaderBytes bounds how much of a file hasGeneratedHeader reads: the marker
// sits in the leading comment block, so a line this long is not a header.
const maxHeaderBytes = 64 * 1024

// hasGeneratedHeader reports whether the file's leading run of blank and `//`
// comment lines contains the generated-code marker (conformist#133). Reading
// stops at the first other line, so only a file's head is read. A file that
// cannot be read is reported as not generated: whatever tool would process it
// surfaces the read error itself.
func hasGeneratedHeader(file *walk.File) bool {
	// In --stdin mode the content lives in TmpPath, not at Path.
	path := file.Path
	if file.TmpPath != "" {
		path = file.TmpPath
	}

	f, err := os.Open(path)
	if err != nil {
		log.Debugf("skip-generated: cannot read %s: %v", file.RelPath, err)

		return false
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), maxHeaderBytes)

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")

		switch {
		case generatedMarker.MatchString(line):
			return true
		case strings.TrimSpace(line) == "", strings.HasPrefix(line, "//"):
			continue
		default:
			return false
		}
	}

	return false
}

// generatedProbe answers "is this file skipped as generated?" at most once per
// file, and only when asked — so a file no formatter or per-file linter wants is
// never opened (conformist#133).
type generatedProbe struct {
	file    *walk.File
	enabled bool

	done, value bool
}

func newGeneratedProbe(file *walk.File, skipGenerated bool) *generatedProbe {
	return &generatedProbe{file: file, enabled: skipGenerated}
}

// skipped reports whether skip-generated withholds the file, reading its
// header on the first call.
func (p *generatedProbe) skipped() bool {
	if !p.enabled {
		return false
	}

	if !p.done {
		p.value, p.done = hasGeneratedHeader(p.file), true
	}

	return p.value
}

// known reports whether a probe already found the file generated, without
// reading it.
func (p *generatedProbe) known() bool { return p.done && p.value }
