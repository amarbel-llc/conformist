package format

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/walk"
	"github.com/charmbracelet/log"
	"github.com/gobwas/glob"
	"mvdan.cc/sh/v3/expand"
)

const (
	BatchSize = 1024
)

var (
	ErrInvalidName = errors.New("formatter name must only contain alphanumeric characters, `_` or `-`")
	// ErrCommandNotFound is returned when the Command for a Formatter is not available.
	ErrCommandNotFound        = errors.New("formatter command not found in PATH")
	ErrNoPositionalArgSupport = errors.New(
		"formatter cannot format multiple files at once (it violates rule 1 of the formatter specification)",
	)

	nameRegex = regexp.MustCompile("^[a-zA-Z0-9_-]+$")
)

// Formatter represents a command which should be applied to a filesystem.
type Formatter struct {
	name   string
	config *config.Formatter

	log        *log.Logger
	commandInv invocation // resolved Command (bare exe or shell line, #38)
	checkInv   invocation // resolved CheckCommand (zero value if none configured)
	treeRoot   string     // the project tree root (working-dir is resolved against it)
	workingDir string     // the dir the tool runs in: treeRoot, or a subdir (#38)

	// internal, compiled versions of Includes and Excludes.
	includes []glob.Glob
	excludes []glob.Glob
}

func (f *Formatter) Name() string {
	return f.name
}

func (f *Formatter) HasNoPositionalArgSupport() bool {
	if f.config.NoPositionalArgSupport == nil {
		return false
	}

	return *f.config.NoPositionalArgSupport
}

func (f *Formatter) Priority() int {
	return f.config.Priority
}

// Hash adds this formatter's config and executable info to the config hash being created.
func (f *Formatter) Hash(h hash.Hash) error {
	// including the name helps us to easily detect when formatters have been added/removed
	h.Write([]byte(f.name))
	// if options change, the outcome of applying the formatter might be different
	h.Write([]byte(strings.Join(f.config.Options, " ")))
	// if priority changes, the outcome of applying a sequence of formatters might be different
	h.Write([]byte(strconv.Itoa(f.config.Priority)))

	// fold in the command's identity: a bare executable's size+mod-time (so a
	// tool upgrade invalidates the cache), or a shell line's text (#38).
	if err := f.commandInv.signature(h); err != nil {
		return err
	}

	return nil
}

func (f *Formatter) Apply(ctx context.Context, files []*walk.File) error {
	if len(files) > 1 && f.HasNoPositionalArgSupport() {
		return ErrNoPositionalArgSupport
	}

	start := time.Now()

	// exit early if nothing to process
	if len(files) == 0 {
		return nil
	}

	// construct args from the configured options, then append the matched file
	// paths, relocated when the formatter runs in a subdir (working-dir, #38);
	// with no working-dir this preserves the historical TmpPath-or-RelPath
	// argument exactly.
	args := append([]string{}, f.config.Options...)
	for _, file := range files {
		args = append(args, relocateFileArg(f.treeRoot, f.workingDir, file.RelPath, file.TmpPath))
	}

	f.log.Debugf("executing: %s %v", f.config.Command, args)

	// a formatter that exits non-zero failed to apply (formatters, unlike
	// linters, must exit 0); surface its output and fail loudly.
	nonzero, out, err := f.commandInv.run(ctx, f.workingDir, args)
	if err != nil || nonzero {
		if len(out) > 0 {
			_, _ = fmt.Fprintf(os.Stderr, "\n%s\n", out)
		}

		if err != nil {
			f.log.Errorf("failed to apply: %s", err)

			return fmt.Errorf("formatter '%s' failed to apply: %w", f.config.Command, err)
		}

		f.log.Errorf("formatter '%s' exited non-zero", f.config.Command)

		return fmt.Errorf("formatter '%s' exited non-zero", f.config.Command)
	}

	f.log.Infof("%v file(s) processed in %v", len(files), time.Since(start))

	return nil
}

// Wants is used to determine if a Formatter wants to process a path based on it's configured Includes and Excludes
// patterns.
// Returns true if the Formatter should be applied to file, false otherwise.
func (f *Formatter) Wants(file *walk.File) bool {
	match := !pathMatches(file.RelPath, f.excludes) && pathMatches(file.RelPath, f.includes)
	if match {
		f.log.Debugf("match: %v", file)
	}

	return match
}

// newFormatter is used to create a new Formatter.
func newFormatter(
	name string,
	treeRoot string,
	env expand.Environ,
	cfg *config.Formatter,
) (*Formatter, error) {
	var err error

	// check the name is valid
	if !nameRegex.MatchString(name) {
		return nil, ErrInvalidName
	}

	f := Formatter{}

	// capture config and the formatter's name
	f.name = name
	f.config = cfg
	f.treeRoot = treeRoot
	f.workingDir = resolveToolDir(treeRoot, cfg.WorkingDir)

	// resolve the command: a bare executable (looked up on PATH) or a shell
	// line run through the interpreter (#38).
	f.commandInv, err = newInvocation(name, treeRoot, env, cfg.Command)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCommandNotFound, err)
	}

	// resolve the optional native check command (RFC 0001 §3)
	if cfg.CheckCommand != "" {
		f.checkInv, err = newInvocation(name+" (check)", treeRoot, env, cfg.CheckCommand)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCommandNotFound, err)
		}
	}

	// initialise internal state
	if cfg.Priority > 0 {
		f.log = log.WithPrefix(fmt.Sprintf("formatter | %s[%d]", name, cfg.Priority))
	} else {
		f.log = log.WithPrefix("formatter | " + name)
	}

	// check there is at least one include
	if len(cfg.Includes) == 0 {
		return nil, fmt.Errorf("formatter '%v' has no includes", f.name)
	}

	f.includes, err = compileGlobs(cfg.Includes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile formatter '%v' includes: %w", f.name, err)
	}

	f.excludes, err = compileGlobs(cfg.Excludes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile formatter '%v' excludes: %w", f.name, err)
	}

	return &f, nil
}
