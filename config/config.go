package config

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/amarbel-llc/conformist/git"
	"github.com/amarbel-llc/conformist/jujutsu"
	"github.com/amarbel-llc/conformist/walk"
	"github.com/charmbracelet/log"
	"github.com/google/shlex"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config is used to represent the list of configured Formatters.
type Config struct {
	AllowMissingFormatter bool     `mapstructure:"allow-missing-formatter" toml:"allow-missing-formatter,omitempty"`
	CI                    bool     `mapstructure:"ci"                      toml:"-"` // not allowed in config
	ClearCache            bool     `mapstructure:"clear-cache"             toml:"-"` // not allowed in config
	CPUProfile            string   `mapstructure:"cpu-profile"             toml:"cpu-profile,omitempty"`
	Excludes              []string `mapstructure:"excludes"                toml:"excludes,omitempty"`
	FailOnChange          bool     `mapstructure:"fail-on-change"          toml:"fail-on-change,omitempty"`
	Formatters            []string `mapstructure:"formatters"              toml:"formatters,omitempty"`
	NoCache               bool     `mapstructure:"no-cache"                toml:"-"` // not allowed in config
	OnUnmatched           string   `mapstructure:"on-unmatched"            toml:"on-unmatched,omitempty"`
	Quiet                 bool     `mapstructure:"quiet"                   toml:"-"` // not allowed in config
	TreeRoot              string   `mapstructure:"tree-root"               toml:"tree-root,omitempty"`
	TreeRootCmd           string   `mapstructure:"tree-root-cmd"           toml:"tree-root-cmd,omitempty"`
	TreeRootFile          string   `mapstructure:"tree-root-file"          toml:"tree-root-file,omitempty"`
	Verbose               uint8    `mapstructure:"verbose"                 toml:"verbose,omitempty"`
	Walk                  string   `mapstructure:"walk"                    toml:"walk,omitempty"`
	WorkingDirectory      string   `mapstructure:"working-dir"             toml:"-"`
	Stdin                 bool     `mapstructure:"stdin"                   toml:"-"` // not allowed in config

	FormatterConfigs map[string]*Formatter `mapstructure:"formatter" toml:"formatter,omitempty"`

	LinterConfigs map[string]*Linter `mapstructure:"linter" toml:"linter,omitempty"`

	Global struct {
		// Deprecated: Use Excludes
		Excludes []string `mapstructure:"excludes" toml:"excludes,omitempty"`
	} `mapstructure:"global" toml:"global,omitempty"`
}

type Formatter struct {
	// Command is the command to invoke when applying this Formatter.
	Command string `mapstructure:"command" toml:"command"`
	// Options are an optional list of args to be passed to Command.
	Options []string `mapstructure:"options,omitempty" toml:"options,omitempty"`
	// Includes is a list of glob patterns used to determine whether this Formatter should be applied against a path.
	Includes []string `mapstructure:"includes,omitempty" toml:"includes,omitempty"`
	// Excludes is an optional list of glob patterns used to exclude certain files from this Formatter.
	Excludes []string `mapstructure:"excludes,omitempty" toml:"excludes,omitempty"`
	// Indicates the order of precedence when executing this Formatter in a sequence of Formatters.
	Priority int `mapstructure:"priority,omitempty" toml:"priority,omitempty"`
	// Does this formatter violate [rule 1] of the formatter spec?
	// [rule 1]: docs/site/reference/formatter-spec.md#1-files-passed-as-arguments
	NoPositionalArgSupport *bool `mapstructure:"no-positional-arg-support" toml:"no-positional-arg-support"`
	// CheckCommand is an optional native read-only check invocation used by
	// `conformist check` instead of the sandbox-and-diff strategy (RFC 0001 §3).
	CheckCommand string `mapstructure:"check-command,omitempty" toml:"check-command,omitempty"`
	// CheckOptions are the args passed to CheckCommand.
	CheckOptions []string `mapstructure:"check-options,omitempty" toml:"check-options,omitempty"`
	// ConfigFiles are filenames the formatter discovers by walking upward from
	// each formatted file (e.g. rustfmt.toml, .editorconfig). In sandbox check
	// mode they are copied from each matched file's ancestor directories into
	// the sandbox at the same relative path, so the sandboxed tool sees the same
	// config the real tree would (conformist#28).
	ConfigFiles []string `mapstructure:"config-files,omitempty" toml:"config-files,omitempty"`
	// Sandbox forces sandbox execution in check mode even when a native check
	// command is available (RFC 0001 §3, §6).
	Sandbox bool `mapstructure:"sandbox,omitempty" toml:"sandbox,omitempty"`
	// WorkingDir is an optional subdirectory (relative to the tree root) in which
	// to run this formatter, e.g. a Go submodule whose codegen must run from
	// there. Empty (the default) runs at the tree root, unchanged. When set, file
	// arguments are passed relative to this directory (conformist#38).
	WorkingDir string `mapstructure:"working-dir,omitempty" toml:"working-dir,omitempty"`
}

// Linter represents a tool that inspects files and reports problems, exiting
// non-zero when problems are found. Configured under [linter.<name>]; see
// docs/rfcs/0001-linter-support-and-check-repair-modes.md (RFC 0001 §4).
type Linter struct {
	// Command is the read-only check invocation, run as
	// `command [options] [...files]`. It MUST NOT write to the files and MUST
	// exit non-zero when at least one passed file has a finding.
	Command string `mapstructure:"command" toml:"command"`
	// Options are an optional list of args passed before the file list.
	Options []string `mapstructure:"options,omitempty" toml:"options,omitempty"`
	// Includes is a list of glob patterns selecting files this Linter inspects.
	Includes []string `mapstructure:"includes,omitempty" toml:"includes,omitempty"`
	// Excludes is an optional list of glob patterns excluding files from this Linter.
	Excludes []string `mapstructure:"excludes,omitempty" toml:"excludes,omitempty"`
	// Priority controls execution order within a file's tool sequence; lower runs first.
	Priority int `mapstructure:"priority,omitempty" toml:"priority,omitempty"`
	// NoPositionalArgSupport indicates the tool cannot process multiple files at once.
	NoPositionalArgSupport *bool `mapstructure:"no-positional-arg-support" toml:"no-positional-arg-support"`
	// PassesFiles controls whether matched file paths are passed to Command as
	// positional arguments. Defaults to true. Set false for a whole-tree /
	// run-once check that operates on the project as a whole and takes no file
	// arguments — it runs once (cwd = tree root) when at least one file matching
	// its includes is present, but never receives a file list (RFC 0001 §4).
	//
	// passes-files also governs the global-excludes interaction (conformist#45):
	// a per-file linter (true) skips globally-excluded files, like formatters,
	// because it would receive the file as input; a whole-tree check (false)
	// matches them, because its includes are only a trigger gate and the file is
	// never passed to it — so it can watch a file formatters may not rewrite,
	// e.g. go.mod.
	PassesFiles *bool `mapstructure:"passes-files,omitempty" toml:"passes-files,omitempty"`
	// RepairCommand is an optional autofix invocation used in repair mode. If
	// unset, the linter is a no-op in repair mode (RFC 0001 §4).
	RepairCommand string `mapstructure:"repair-command,omitempty" toml:"repair-command,omitempty"`
	// RepairOptions are the args passed to RepairCommand.
	RepairOptions []string `mapstructure:"repair-options,omitempty" toml:"repair-options,omitempty"`
	// RestageRepairOutputs widens the --staged hook's restage scope for a
	// whole-tree codegen-repair linter (conformist#55). The --staged restage
	// otherwise covers only files that were already in the index, which strands
	// the outputs of a whole-tree (passes-files=false) repair-command that
	// regenerates files OTHER than the staged ones (e.g. editing a staged source
	// file makes a generated sibling stale; the repair regenerates the sibling,
	// but it was never staged so the commit lands stale). When this is true on
	// such a linter, --staged also restages the paths the linter's repair-command
	// actually wrote — detected by a git-status delta around that linter's
	// repair, so only its own writes are captured (not another linter's). Default
	// false: repair outputs stay scoped to the staged set like formatter output.
	// Has effect only on a whole-tree linter (passes-files=false) with a
	// repair-command; ignored otherwise.
	RestageRepairOutputs bool `mapstructure:"restage-repair-outputs,omitempty" toml:"restage-repair-outputs,omitempty"`
	// StageNewOutputs widens the --staged restage scope further (tier 3,
	// conformist#56, RFC-0002 §2.3): when set alongside RestageRepairOutputs on a
	// whole-tree codegen-repair linter, --staged also stages brand-new
	// (untracked) files the linter's repair-command created, not just the
	// modified tracked outputs tier 2 covers. Staging untracked files is more
	// dangerous than restaging tracked ones — a buggy codegen could sweep
	// unintended new files into history — so it is a distinct opt-in and has
	// effect only when RestageRepairOutputs is also true; ignored otherwise.
	// Attribution is still by git-status delta around the linter's repair, so a
	// pre-existing untracked file is never swept in. Default false.
	StageNewOutputs bool `mapstructure:"stage-new-outputs,omitempty" toml:"stage-new-outputs,omitempty"`
	// StageDeletedOutputs widens the --staged restage scope further still
	// (tier 4, conformist#57, RFC-0002 §2.4): when set alongside
	// RestageRepairOutputs on a whole-tree codegen-repair linter, --staged stages
	// the deletions the linter's repair-command performed (e.g. a package-move
	// codegen that removes a relocated file), so the removal is part of the commit
	// rather than left as an unstaged deletion. Staging a deletion removes a path
	// from the commit's tree — the most destructive stage mutation — so it is a
	// distinct opt-in: tiers 2 and 3 MUST NOT stage deletions, only this tier
	// does. Has effect only when RestageRepairOutputs is also true; ignored
	// otherwise. Attribution is by the same git-status delta. Default false.
	StageDeletedOutputs bool `mapstructure:"stage-deleted-outputs,omitempty" toml:"stage-deleted-outputs,omitempty"`
	// WorkingDir is an optional subdirectory (relative to the tree root) in which
	// to run this linter's check/repair commands, e.g. a Go submodule whose
	// codegen must run from there. Empty (the default) runs at the tree root,
	// unchanged. When set, file arguments are passed relative to this directory
	// (conformist#38).
	WorkingDir string `mapstructure:"working-dir,omitempty" toml:"working-dir,omitempty"`
}

// SetFlags appends our flags to the provided flag set.
// We have a flag matching most entries in Config, taking care to ensure the name matches the field name defined in the
// mapstructure tag.
// We rely on a flag's default value being provided in the event the same value was not specified in the config file.
func SetFlags(fs *pflag.FlagSet) {
	fs.Bool(
		"allow-missing-formatter", false,
		"Do not exit with error if a configured formatter is missing. (env $CONFORMIST_ALLOW_MISSING_FORMATTER)",
	)
	fs.Bool(
		"ci", false,
		"Runs conformist in a CI mode, enabling --no-cache, --fail-on-change and adjusting some other settings "+
			"best suited to a CI use case. (env $CONFORMIST_CI)",
	)
	fs.BoolP(
		"clear-cache", "c", false,
		"Reset the evaluation cache. Use in case the cache is not precise enough. (env $CONFORMIST_CLEAR_CACHE)",
	)
	fs.String(
		"cpu-profile", "",
		"The file into which a cpu profile will be written. (env $CONFORMIST_CPU_PROFILE)",
	)
	fs.StringSlice(
		"excludes", nil,
		"Exclude files or directories matching the specified globs. (env $CONFORMIST_EXCLUDES)",
	)
	fs.Bool(
		"fail-on-change", false,
		"Exit with error if any changes were made. Useful for CI. (env $CONFORMIST_FAIL_ON_CHANGE)",
	)
	fs.StringSliceP(
		"formatters", "f", nil,
		"Specify formatters to apply. Defaults to all configured formatters. (env $CONFORMIST_FORMATTERS)",
	)
	fs.Bool(
		"no-cache", false,
		"Ignore the evaluation cache entirely. Useful for CI. (env $CONFORMIST_NO_CACHE)",
	)
	fs.StringP(
		"on-unmatched", "u", "info",
		"Log paths that did not match any formatters at the specified log level. Possible values: "+
			"debug, info, warn, error, fatal. (env $CONFORMIST_ON_UNMATCHED)",
	)
	fs.Bool(
		"stdin", false,
		"Format the context passed in via stdin.",
	)
	fs.String(
		"tree-root", "",
		"The root directory from which conformist will start walking the filesystem. "+
			"Defaults to the root of the current git or jujutsu worktree. If not in a git or jujutsu repo, defaults to the "+
			"directory containing the config file. (env $CONFORMIST_TREE_ROOT)",
	)
	fs.String(
		"tree-root-cmd", "",
		"Command to run to find the tree root. It is parsed using shlex, to allow quoting arguments that "+
			"contain whitespace. If you wish to pass arguments containing quotes, you should use nested quotes "+
			"e.g. \"'\" or '\"'. (env $CONFORMIST_TREE_ROOT_CMD)",
	)
	fs.String(
		"tree-root-file", "",
		"File to search for to find the tree root. (env $CONFORMIST_TREE_ROOT_FILE)",
	)
	fs.CountP(
		"verbose", "v",
		"Set the verbosity of logs e.g. -vv. (env $CONFORMIST_VERBOSE)",
	)
	fs.BoolP(
		"quiet", "q", false, "Disable all logs except errors. (env $CONFORMIST_QUIET)",
	)
	fs.String(
		"walk", "auto",
		"The method used to traverse the files within the tree root. Currently supports "+
			"auto, git, jujutsu, or filesystem. (env $CONFORMIST_WALK)",
	)
	fs.StringP(
		"working-dir", "C", ".",
		"Run as if conformist was started in the specified working directory instead of the current working "+
			"directory. (env $CONFORMIST_WORKING_DIR)",
	)
}

// NewViper creates a Viper instance pre-configured with the following options:
//   - TOML config type
//   - automatic env enabled
//   - `CONFORMIST_` env prefix for environment variables, falling back to the
//     legacy `TREELINT_` prefix when the `CONFORMIST_` equivalent is unset
//   - replacement of `-` and `.` with `_` when mapping flags to env, e.g.
//     `global.excludes` => `CONFORMIST_GLOBAL_EXCLUDES`.
func NewViper() (*viper.Viper, error) {
	v := viper.New()

	// Enforce toml (may open this up to other formats in the future)
	v.SetConfigType("toml")

	// Backward-compat: honour the legacy TREELINT_ env prefix when the
	// CONFORMIST_ equivalent is unset, so existing TREELINT_* environment
	// variables keep working after the treelint -> conformist rename.
	if err := applyLegacyEnvPrefix(); err != nil {
		return nil, err
	}

	// Allow env overrides for config and flags.
	v.SetEnvPrefix("conformist")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))

	// unset some env variables that we don't want automatically applied, under
	// both the current and legacy prefixes
	for _, name := range []string{"CONFORMIST_STDIN", "TREELINT_STDIN"} {
		if err := os.Unsetenv(name); err != nil {
			return nil, fmt.Errorf("failed to unset %s: %w", name, err)
		}
	}

	return v, nil
}

// applyLegacyEnvPrefix copies any TREELINT_<suffix> environment variable to
// CONFORMIST_<suffix> when the latter is unset, preserving support for the
// pre-rename env prefix. The current CONFORMIST_ prefix always takes precedence.
func applyLegacyEnvPrefix() error {
	const oldPrefix, newPrefix = "TREELINT_", "CONFORMIST_"

	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}

		suffix, ok := strings.CutPrefix(name, oldPrefix)
		if !ok {
			continue
		}

		target := newPrefix + suffix
		if _, set := os.LookupEnv(target); set {
			continue
		}

		if err := os.Setenv(target, value); err != nil {
			return fmt.Errorf("failed to map %s to %s: %w", name, target, err)
		}
	}

	return nil
}

// FromViper takes a viper instance and produces a Config instance.
func FromViper(v *viper.Viper) (*Config, error) {
	logger := log.WithPrefix("config")

	configReset := map[string]any{
		"ci":          false,
		"clear-cache": false,
		"no-cache":    false,
		"stdin":       false,
		"working-dir": ".",
	}

	// reset certain values which are not allowed to be specified in the config file
	if err := v.MergeConfigMap(configReset); err != nil {
		return nil, fmt.Errorf("failed to overwrite config values: %w", err)
	}

	// read config from viper
	var err error

	cfg := &Config{}

	if err = v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// resolve the working directory to an absolute path
	cfg.WorkingDirectory, err = filepath.Abs(cfg.WorkingDirectory)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for working directory: %w", err)
	}

	// if the stdin flag was passed, we force the stdin walk type
	if cfg.Stdin {
		cfg.Walk = walk.Stdin.String()
	}

	// determine tree root
	if err = determineTreeRoot(v, cfg, logger); err != nil {
		return nil, fmt.Errorf("failed to determine tree root: %w", err)
	}

	// prefer top level excludes, falling back to global.excludes for backwards compatibility
	if len(cfg.Excludes) == 0 {
		cfg.Excludes = cfg.Global.Excludes
	}

	// validate formatter names do not contain invalid characters
	nameRegex := regexp.MustCompile("^[a-zA-Z0-9_-]+$")

	for name := range cfg.FormatterConfigs {
		if !nameRegex.MatchString(name) {
			return nil, fmt.Errorf(
				"formatter name %q is invalid, must be of the form %s",
				name, nameRegex.String(),
			)
		}
	}

	// validate linter names do not contain invalid characters (RFC 0001 §4)
	for name := range cfg.LinterConfigs {
		if !nameRegex.MatchString(name) {
			return nil, fmt.Errorf(
				"linter name %q is invalid, must be of the form %s",
				name, nameRegex.String(),
			)
		}
	}

	// filter formatters based on provided names
	if len(cfg.Formatters) > 0 {
		filtered := make(map[string]*Formatter)

		// check if the provided names exist in the config
		for _, name := range cfg.Formatters {
			if !nameRegex.MatchString(name) {
				return nil, fmt.Errorf(
					"formatter name %q is invalid, must be of the form %s",
					name, nameRegex.String(),
				)
			}

			formatterCfg, ok := cfg.FormatterConfigs[name]
			if !ok {
				return nil, fmt.Errorf("formatter %v not found in config", name)
			}

			filtered[name] = formatterCfg
		}

		// updated formatters
		cfg.FormatterConfigs = filtered
	}

	// ci mode
	if cfg.CI {
		cfg.NoCache = true
		cfg.FailOnChange = true

		// ensure at least info level logging
		if cfg.Verbose < 1 {
			cfg.Verbose = 1
		}
	}

	return cfg, nil
}

func determineTreeRoot(v *viper.Viper, cfg *Config, logger *log.Logger) error {
	var err error

	// enforce the various tree root options are mutually exclusive
	// some of this is being done for us at the flag level, but you can also set these values in config or environment
	// variables.
	count := 0

	if cfg.TreeRoot != "" {
		count++
	}

	if cfg.TreeRootCmd != "" {
		count++
	}

	if cfg.TreeRootFile != "" {
		count++
	}

	if count > 1 {
		return errors.New("at most one of tree-root, tree-root-cmd or tree-root-file can be specified")
	}

	switch {
	case cfg.TreeRoot != "":
		logger.Infof("tree root specified explicitly: %s", cfg.TreeRoot)

	case cfg.TreeRootFile != "":
		logger.Infof("searching for tree root using tree-root-file: %s", cfg.TreeRootFile)

		_, cfg.TreeRoot, err = FindUp(cfg.WorkingDirectory, cfg.TreeRootFile)
		if err != nil {
			return fmt.Errorf("failed to find tree-root based on tree-root-file: %w", err)
		}

	case cfg.TreeRootCmd != "":
		logger.Infof("searching for tree root using tree-root-cmd: %s", cfg.TreeRootCmd)

		if cfg.TreeRoot, err = execTreeRootCmd(cfg.TreeRootCmd, cfg.WorkingDirectory); err != nil {
			return err
		}

	default:
		// no tree root was specified
		logger.Infof("no tree root specified")

		// attempt to resolve with git
		if cfg.Walk == walk.Auto.String() || cfg.Walk == walk.Git.String() {
			logger.Infof("attempting to resolve tree root using git: %s", git.TreeRootCmd)

			// attempt to resolve the tree root with git
			cfg.TreeRoot, err = execTreeRootCmd(git.TreeRootCmd, cfg.WorkingDirectory)
			if err != nil && cfg.Walk == walk.Git.String() {
				return fmt.Errorf("failed to resolve tree root with git: %w", err)
			}

			if err != nil {
				logger.Infof("failed to resolve tree root with git: %v", err)
			}
		}

		// attempt to resolve with jujutsu
		if cfg.TreeRoot == "" && (cfg.Walk == walk.Auto.String() || cfg.Walk == walk.Jujutsu.String()) {
			logger.Infof("attempting to resolve tree root using jujutsu: %s", jujutsu.TreeRootCmd)

			// attempt to resolve the tree root with jujutsu
			cfg.TreeRoot, err = execTreeRootCmd(jujutsu.TreeRootCmd, cfg.WorkingDirectory)
			if err != nil && cfg.Walk == walk.Git.String() {
				return fmt.Errorf("failed to resolve tree root with jujutsu: %w", err)
			}

			if err != nil {
				logger.Infof("failed to resolve tree root with jujutsu: %v", err)
			}
		}

		if cfg.TreeRoot == "" {
			configDir := filepath.Dir(v.ConfigFileUsed())

			// No --tree-root and no git/jujutsu repo. A config discovered in-tree
			// sits at or above the working directory and is a good project-root
			// proxy, so use its directory — this keeps `conformist` run from a
			// subdirectory walking the whole tree. But an explicit, out-of-tree
			// --config-file (e.g. a /nix/store path) must NOT silently redirect
			// the walk to its own directory; fall back to the working directory
			// instead. See amarbel-llc/conformist#2.
			if dirContains(configDir, cfg.WorkingDirectory) {
				logger.Infof(
					"no git/jujutsu repo found; tree root defaults to the config file's directory: %s",
					configDir,
				)

				cfg.TreeRoot = configDir
			} else {
				logger.Warnf(
					"no git/jujutsu repo found and config file %q is outside the working "+
						"directory; tree root defaults to the working directory %q — pass "+
						"--tree-root to override",
					v.ConfigFileUsed(), cfg.WorkingDirectory,
				)

				cfg.TreeRoot = cfg.WorkingDirectory
			}
		}
	}

	// resolve tree root to an absolute path
	if cfg.TreeRoot, err = filepath.Abs(cfg.TreeRoot); err != nil {
		return fmt.Errorf("failed to get absolute path for tree root: %w", err)
	}

	logger.Infof("tree root: %s", cfg.TreeRoot)

	return nil
}

func execTreeRootCmd(treeRootCmd string, workingDir string) (string, error) {
	// split the command first, resolving any '' and "" entries
	parts, splitErr := shlex.Split(treeRootCmd)
	if splitErr != nil {
		return "", fmt.Errorf("failed to parse tree-root-cmd: %w", splitErr)
	}

	// set a reasonable timeout of 2 seconds to wait for the command to return
	// it shouldn't take anywhere near this amount of time unless there's a problem
	executionTimeout := 2 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), executionTimeout)
	defer cancel()

	// construct the command, setting the correct working directory
	//nolint:gosec
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Dir = workingDir

	// setup some pipes to capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe for tree-root-cmd: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stderr pipe for tree-root-cmd: %w", err)
	}

	// start processing stderr before we begin executing the command
	go func() {
		// capture stderr line by line and log
		l := log.WithPrefix("tree-root-cmd | stderr")

		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			l.Debugf("%s", scanner.Text())
		}
	}()

	// start executing without waiting
	if cmdErr := cmd.Start(); cmdErr != nil {
		return "", fmt.Errorf("failed to start tree-root-cmd: %w", cmdErr)
	}

	// read stdout until it is closed (command exits)
	output, err := io.ReadAll(stdout)
	if err != nil {
		return "", fmt.Errorf("failed to read stdout from tree-root-cmd: %w", err)
	}

	log.WithPrefix("tree-root-cmd | stdout").Debugf("%s", output)

	// check execution error
	if cmdErr := cmd.Wait(); cmdErr != nil {
		var exitErr *exec.ExitError

		// by experimenting, I noticed that sometimes we received the deadline exceeded error first, other times
		// the exit error indicating the process was killed, therefore, we look for both
		tookTooLong := errors.Is(cmdErr, context.DeadlineExceeded)
		tookTooLong = tookTooLong || (errors.As(cmdErr, &exitErr) && exitErr.String() == "signal: killed")

		if tookTooLong {
			return "", fmt.Errorf(
				"tree-root-cmd was killed after taking more than %v to execute",
				executionTimeout,
			)
		}

		// otherwise, some other kind of error occurred
		return "", fmt.Errorf("failed to execute tree-root-cmd: %w", cmdErr)
	}

	// validate the output
	outputStr := string(output)

	lines := strings.Split(outputStr, "\n")
	nonEmptyLines := slices.DeleteFunc(lines, func(line string) bool {
		return line == ""
	})

	switch len(nonEmptyLines) {
	case 1:
		// return the first line as the tree root
		return nonEmptyLines[0], nil

	case 0:
		// no output was received on stdout
		return "", fmt.Errorf("empty output received after executing tree-root-cmd: %s", treeRootCmd)

	default:
		// multiple lines received on stdout, dump the output to make it clear what happened and throw an error
		log.WithPrefix("tree-root-cmd | stdout").Errorf("\n%s", outputStr)

		return "", fmt.Errorf("tree-root-cmd cannot output multiple lines: %s", treeRootCmd)
	}
}

func Find(searchDir string, fileNames ...string) (path string, err error) {
	for _, f := range fileNames {
		path := filepath.Join(searchDir, f)
		if fileExists(path) {
			return path, nil
		}
	}

	return "", fmt.Errorf("could not find %s in %s", fileNames, searchDir)
}

func FindUp(searchDir string, fileNames ...string) (path string, dir string, err error) {
	for _, dir := range eachDir(searchDir) {
		path, err := Find(dir, fileNames...)
		if err == nil {
			return path, dir, nil
		}
	}

	return "", "", fmt.Errorf("could not find %s in %s", fileNames, searchDir)
}

// dirContains reports whether child is parent or a descendant of parent. Both
// paths are expected to be absolute and cleaned (as cfg.TreeRoot/WorkingDirectory
// and filepath.Dir(ConfigFileUsed()) already are).
func dirContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}

	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// configCeilingDirs parses CONFORMIST_CEILING_DIRECTORIES into the set of
// directories at which upward discovery (eachDir) must stop. It mirrors git's
// GIT_CEILING_DIRECTORIES semantics: a colon-separated list of absolute paths;
// each entry is symlink-resolved for comparison, except that an empty entry
// turns off resolution for the entries that follow it (so a leading empty entry
// marks subsequent paths as already-canonical, as in
// `/maybe/symlink::/very/slow/non/symlink`). Relative entries are ignored.
//
// As with git, the legacy TREELINT_ prefix is honored transitively: NewViper
// copies any TREELINT_CEILING_DIRECTORIES into CONFORMIST_CEILING_DIRECTORIES.
func configCeilingDirs() map[string]struct{} {
	raw := os.Getenv("CONFORMIST_CEILING_DIRECTORIES")
	if raw == "" {
		return nil
	}

	set := make(map[string]struct{})
	resolve := true

	for entry := range strings.SplitSeq(raw, string(os.PathListSeparator)) {
		if entry == "" {
			// An empty entry disables symlink resolution for subsequent entries.
			resolve = false

			continue
		}

		if !filepath.IsAbs(entry) {
			continue
		}

		if resolve {
			if resolved, err := filepath.EvalSymlinks(entry); err == nil {
				entry = resolved
			}
		}

		set[filepath.Clean(entry)] = struct{}{}
	}

	return set
}

// eachDir returns searchDir followed by each of its ancestor directories, up
// toward the filesystem root. The upward walk stops before entering any
// directory named in CONFORMIST_CEILING_DIRECTORIES, mirroring how git's
// GIT_CEILING_DIRECTORIES bounds its `.git` search: a ceiling directory (and
// everything above it) is not visited, but searchDir itself is always included
// even if it is a ceiling. With no ceiling set the walk reaches the root, so the
// default behavior is unchanged.
func eachDir(path string) (paths []string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}

	ceilings := configCeilingDirs()

	// Compare canonical paths against the resolved ceilings, as git compares
	// against the real working directory. Only do this when a ceiling is in
	// play, so the ceiling-free path stays byte-for-byte as before.
	if len(ceilings) > 0 {
		if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
			abs = resolved
		}
	}

	root := string(os.PathSeparator)
	paths = []string{abs}
	path = abs

	for path != root {
		parent := filepath.Dir(path)
		if parent == path {
			break
		}

		if _, blocked := ceilings[parent]; blocked {
			break
		}

		paths = append(paths, parent)
		path = parent
	}

	return paths
}

func fileExists(path string) bool {
	// Some broken filesystems like SSHFS return file information on stat() but
	// then cannot open the file. So we use os.Open.
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	// fileExists only stats a read-only fd and returns a bool, so there is no
	// error channel to carry a Close failure and the error is benign.
	defer f.Close() //defer:err-checked

	// Next, check that the file is a regular file.
	fi, err := f.Stat()
	if err != nil {
		return false
	}

	return fi.Mode().IsRegular()
}
