package config_test

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func newViper(t *test_ui.T) (*viper.Viper, *pflag.FlagSet) {
	t.Helper()

	v, err := config.NewViper()
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	v.SetConfigFile(filepath.Join(tempDir, "conformist.toml"))

	// initialise a git repo to help with tree-root-cmd testing
	cmd := exec.CommandContext(t.Context(), "git", "init")
	cmd.Dir = tempDir

	if err = cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// change working directory to the temp dir
	t.Chdir(tempDir)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.SetFlags(flags)

	if err := v.BindPFlags(flags); err != nil {
		t.Fatal(err)
	}

	return v, flags
}

func writeAndReadBack(t *test_ui.T, v *viper.Viper, cfg *config.Config) {
	t.Helper()

	// serialise the config and read it into viper
	buf := bytes.NewBuffer(nil)

	encoder := toml.NewEncoder(buf)
	if err := encoder.Encode(cfg); err != nil {
		t.Fatal(fmt.Errorf("failed to marshal config: %w", err))
	} else if err = v.ReadConfig(bufio.NewReader(buf)); err != nil {
		t.Fatal(fmt.Errorf("failed to read config: %w", err))
	}
}

func TestLinterConfig(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	v, _ := newViper(t)

	cfg := &config.Config{
		LinterConfigs: map[string]*config.Linter{
			"shellcheck": {
				Command:  "shellcheck",
				Includes: []string{"*.sh"},
			},
			"ruff": {
				Command:       "ruff",
				Options:       []string{"check"},
				Includes:      []string{"*.py"},
				RepairCommand: "ruff",
				RepairOptions: []string{"check", "--fix"},
			},
			"drift": {
				Command:     "dagnabit",
				Options:     []string{"export", "--check"},
				Includes:    []string{"libs/**"},
				PassesFiles: ptr(false),
			},
		},
	}

	writeAndReadBack(t, v, cfg)

	decoded, err := config.FromViper(v)
	as.NoError(err)
	as.Len(decoded.LinterConfigs, 3)

	sc := decoded.LinterConfigs["shellcheck"]
	as.NotNil(sc)
	as.Equal("shellcheck", sc.Command)
	as.Equal([]string{"*.sh"}, sc.Includes)

	ruff := decoded.LinterConfigs["ruff"]
	as.NotNil(ruff)
	as.Equal("ruff", ruff.RepairCommand)
	as.Equal([]string{"check", "--fix"}, ruff.RepairOptions)

	// a whole-tree check round-trips passes-files=false; the per-file linters
	// leave it unset (nil => defaults to true). passes-files also governs the
	// global-excludes interaction now (conformist#45), so there is no separate
	// ignore-global-excludes field to round-trip.
	drift := decoded.LinterConfigs["drift"]
	as.NotNil(drift)
	as.NotNil(drift.PassesFiles)
	as.False(*drift.PassesFiles)
	as.Nil(sc.PassesFiles)
}

func ptr[T any](v T) *T { return &v }

func readError(t *test_ui.T, v *viper.Viper, cfg *config.Config, test func(error)) {
	t.Helper()

	writeAndReadBack(t, v, cfg)

	_, err := config.FromViper(v)
	if err == nil {
		t.Fatal("error was expected but none was thrown")
	}

	test(err)
}

func readValue(t *test_ui.T, v *viper.Viper, cfg *config.Config, test func(*config.Config)) {
	t.Helper()

	writeAndReadBack(t, v, cfg)

	//
	decodedCfg, err := config.FromViper(v)
	if err != nil {
		t.Fatal(fmt.Errorf("failed to unmarshal config from viper: %w", err))
	}

	test(decodedCfg)
}

func TestAllowMissingFormatter(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected bool) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.AllowMissingFormatter)
		})
	}

	// default with no flag, env or config
	checkValue(false)

	// set config value
	cfg.AllowMissingFormatter = true

	checkValue(true)

	// env override
	t.Setenv("CONFORMIST_ALLOW_MISSING_FORMATTER", "false")
	checkValue(false)

	// flag override
	as.NoError(flags.Set("allow-missing-formatter", "true"))
	checkValue(true)
}

func TestCI(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValues := func(ci bool, noCache bool, failOnChange bool, verbosity uint8) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(ci, cfg.CI)
			as.Equal(noCache, cfg.NoCache)
			as.Equal(failOnChange, cfg.FailOnChange)
			as.Equal(verbosity, cfg.Verbose)
		})
	}

	// default with no flag, env or config
	checkValues(false, false, false, 0)

	// set config value and check that it has no effect
	// you are not allowed to set ci in config
	cfg.CI = true

	checkValues(false, false, false, 0)

	// env override
	t.Setenv("CONFORMIST_CI", "false")
	checkValues(false, false, false, 0)

	// flag override
	as.NoError(flags.Set("ci", "true"))
	checkValues(true, true, true, 1)

	// increase verbosity above 1 and check it isn't reset
	cfg.Verbose = 2

	checkValues(true, true, true, 2)
}

func TestClearCache(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected bool) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.ClearCache)
		})
	}

	// default with no flag, env or config
	checkValue(false)

	// set config value and check that it has no effect
	// you are not allowed to set clear-cache in config
	cfg.ClearCache = true

	checkValue(false)

	// env override
	t.Setenv("CONFORMIST_CLEAR_CACHE", "false")
	checkValue(false)

	// flag override
	as.NoError(flags.Set("clear-cache", "true"))
	checkValue(true)
}

func TestCpuProfile(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected string) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.CPUProfile)
		})
	}

	// default with no flag, env or config
	checkValue("")

	// set config value
	cfg.CPUProfile = "/foo/bar"

	checkValue("/foo/bar")

	// env override
	t.Setenv("CONFORMIST_CPU_PROFILE", "/fizz/buzz")
	checkValue("/fizz/buzz")

	// flag override
	as.NoError(flags.Set("cpu-profile", "/bla/bla"))
	checkValue("/bla/bla")
}

func TestExcludes(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected []string) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.Excludes)
		})
	}

	// default with no env or config
	checkValue(nil)

	// set config value
	cfg.Excludes = []string{"foo", "bar"}

	checkValue([]string{"foo", "bar"})

	// test global.excludes fallback
	cfg.Excludes = nil
	cfg.Global.Excludes = []string{"fizz", "buzz"}

	checkValue([]string{"fizz", "buzz"})

	// env override
	t.Setenv("CONFORMIST_EXCLUDES", "foo,bar")
	checkValue([]string{"foo", "bar"})

	// flag override
	as.NoError(flags.Set("excludes", "bleep,bloop"))
	checkValue([]string{"bleep", "bloop"})
}

func TestFailOnChange(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected bool) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.FailOnChange)
		})
	}

	// default with no flag, env or config
	checkValue(false)

	// set config value
	cfg.FailOnChange = true

	checkValue(true)

	// env override
	t.Setenv("CONFORMIST_FAIL_ON_CHANGE", "false")
	checkValue(false)

	// flag override
	as.NoError(flags.Set("fail-on-change", "true"))
	checkValue(true)
}

func TestFormatters(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected []string) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.Formatters)
		})
	}

	// default with no env or config
	checkValue([]string{})

	// set config value
	cfg.FormatterConfigs = map[string]*config.Formatter{
		"echo": {
			Command: "echo",
		},
		"touch": {
			Command: "touch",
		},
		"date": {
			Command: "date",
		},
	}

	cfg.Formatters = []string{"echo", "touch"}

	checkValue([]string{"echo", "touch"})

	// env override
	t.Setenv("CONFORMIST_FORMATTERS", "echo,date")
	checkValue([]string{"echo", "date"})

	// flag override
	as.NoError(flags.Set("formatters", "date,touch"))
	checkValue([]string{"date", "touch"})

	// bad formatter name
	as.NoError(flags.Set("formatters", "foo,echo,date"))

	_, err := config.FromViper(v)
	as.ErrorContains(err, "formatter foo not found in config")
}

func TestNoCache(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected bool) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.NoCache)
		})
	}

	// default with no flag, env or config
	checkValue(false)

	// set config value and check that it has no effect
	// you are not allowed to set no-cache in config
	cfg.NoCache = true

	checkValue(false)

	// env override
	t.Setenv("CONFORMIST_NO_CACHE", "false")
	checkValue(false)

	// flag override
	as.NoError(flags.Set("no-cache", "true"))
	checkValue(true)
}

func TestQuiet(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected bool) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.Quiet)
		})
	}

	// default with no flag, env or config
	checkValue(false)

	// set config value and check that it has no effect
	// you are not allowed to set no-cache in config
	cfg.Quiet = true

	checkValue(false)

	// env override
	t.Setenv("CONFORMIST_QUIET", "false")
	checkValue(false)

	// flag override
	as.NoError(flags.Set("quiet", "true"))
	checkValue(true)
}

func TestOnUnmatched(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected string) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.OnUnmatched)
		})
	}

	// default with no flag, env or config
	checkValue("info")

	// set config value
	cfg.OnUnmatched = "error"

	checkValue("error")

	// env override
	t.Setenv("CONFORMIST_ON_UNMATCHED", "debug")
	checkValue("debug")

	// flag override
	as.NoError(flags.Set("on-unmatched", "fatal"))
	checkValue("fatal")
}

func TestTreeRoot(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected string) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.TreeRoot)
		})
	}

	// default with no flag, env or config
	// should match the absolute path of the directory in which the config file is located
	checkValue(filepath.Dir(v.ConfigFileUsed()))

	// set config value
	cfg.TreeRoot = "/foo/bar"

	checkValue("/foo/bar")

	// env override
	t.Setenv("CONFORMIST_TREE_ROOT", "/fizz/buzz")
	checkValue("/fizz/buzz")

	// flag override
	as.NoError(flags.Set("tree-root", "/flip/flop"))
	checkValue("/flip/flop")
}

func TestTreeRootFile(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	// create a directory structure with config files at various levels
	tempDir := t.TempDir()
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "foo", "bar"), 0o755))
	as.NoError(os.WriteFile(filepath.Join(tempDir, "foo", "bar", "a.txt"), []byte{}, 0o600))
	as.NoError(os.WriteFile(filepath.Join(tempDir, "foo", "go.mod"), []byte{}, 0o600))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, ".git"), 0o755))
	as.NoError(os.WriteFile(filepath.Join(tempDir, ".git", "config"), []byte{}, 0o600))

	checkValue := func(treeRoot string, treeRootFile string) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(treeRoot, cfg.TreeRoot)
			as.Equal(treeRootFile, cfg.TreeRootFile)
		})
	}

	// default with no flag, env or config
	// should match the absolute path of the directory in which the config file is located
	checkValue(filepath.Dir(v.ConfigFileUsed()), "")

	workDir := filepath.Join(tempDir, "foo", "bar")
	t.Setenv("CONFORMIST_WORKING_DIR", workDir)

	// set config value
	// should match the lowest directory
	cfg.TreeRootFile = "a.txt"

	checkValue(workDir, "a.txt")

	// env override
	// should match the directory above
	t.Setenv("CONFORMIST_TREE_ROOT_FILE", "go.mod")
	checkValue(filepath.Join(tempDir, "foo"), "go.mod")

	// flag override
	// should match the root of the temp directory structure
	as.NoError(flags.Set("tree-root-file", ".git/config"))
	checkValue(tempDir, ".git/config")
}

func TestTreeRootCmd(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(treeRoot string) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(treeRoot, cfg.TreeRoot)
		})
	}

	tempDir := t.TempDir()
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "foo"), 0o755))
	as.NoError(os.MkdirAll(filepath.Join(tempDir, "bar"), 0o755))

	// default with no flag, env or config
	// should match the absolute path of the directory in which the config file is located
	checkValue(filepath.Dir(v.ConfigFileUsed()))

	// set config value
	cfg.TreeRootCmd = "echo " + tempDir
	checkValue(tempDir)

	// env override
	// should match the directory above
	t.Setenv("CONFORMIST_TREE_ROOT_CMD", fmt.Sprintf("echo \"%s/foo\"", tempDir))
	checkValue(filepath.Join(tempDir, "foo"))

	// flag override
	// should match the root of the temp directory structure
	as.NoError(flags.Set("tree-root-cmd", fmt.Sprintf("echo '%s/bar'", tempDir)))
	checkValue(filepath.Join(tempDir, "bar"))

	// empty output from tree-root-cmd
	// should throw an error
	as.NoError(flags.Set("tree-root-cmd", "echo ''"))
	readError(t, v, cfg, func(err error) {
		as.ErrorContains(err, "empty output received after executing tree-root-cmd: echo ''")
	})
}

// TestTreeRootFallbackToWorkingDir verifies that when no tree root is specified
// and no git/jujutsu repo is found, the tree root defaults to the working
// directory rather than the config file's directory. An out-of-tree
// --config-file (e.g. a /nix/store path) must not silently redirect the walk.
// See amarbel-llc/conformist#2.
func TestTreeRootFallbackToWorkingDir(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, _ := newViper(t)

	// The config file lives in newViper's tempDir; point the working directory
	// at a separate, unrelated directory.
	workDir := t.TempDir()
	t.Setenv("CONFORMIST_WORKING_DIR", workDir)

	// The filesystem walk skips git/jujutsu detection, forcing the no-repo
	// fallback regardless of whether the tempdirs sit inside a repo.
	t.Setenv("CONFORMIST_WALK", "filesystem")

	readValue(t, v, cfg, func(cfg *config.Config) {
		as.Equal(workDir, cfg.TreeRoot)
		as.NotEqual(
			filepath.Dir(v.ConfigFileUsed()), cfg.TreeRoot,
			"tree root must not fall back to the config file's directory",
		)
	})
}

func TestVerbosity(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, _ := newViper(t)

	checkValue := func(expected uint8) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.Verbose)
		})
	}

	// default with no flag, env or config
	checkValue(0)

	// set config value
	cfg.Verbose = 1

	checkValue(1)

	// flag override
	// todo unsure how to set a count flag via the flags api
	// as.NoError(flags.Set("verbose", "v"))
	// checkValue(1)

	// env override
	t.Setenv("CONFORMIST_VERBOSE", "2")
	checkValue(2)
}

func TestWalk(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected string) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.Walk)
		})
	}

	// default with no flag, env or config
	checkValue("auto")

	// set config value
	cfg.Walk = "git"

	checkValue("git")

	// env override
	t.Setenv("CONFORMIST_WALK", "filesystem")
	checkValue("filesystem")

	// flag override
	as.NoError(flags.Set("walk", "auto"))
	checkValue("auto")
}

func TestWorkingDirectory(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValue := func(expected string) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(expected, cfg.WorkingDirectory)
		})
	}

	cwd, err := os.Getwd()
	as.NoError(err, "failed to get current working directory")
	cwd, err = filepath.Abs(cwd)
	as.NoError(err, "failed to get absolute path of current working directory")

	// default with no flag, env or config
	// current working directory by default
	checkValue(cwd)

	// set config value and check that it has no effect
	// you are not allowed to set working-dir in config
	cfg.WorkingDirectory = "/foo/bar/baz/../fizz"

	checkValue(cwd)

	// env override
	cwd = t.TempDir()
	t.Setenv("CONFORMIST_WORKING_DIR", cwd+"/buzz/..")
	checkValue(cwd)

	// flag override
	cwd = t.TempDir()
	as.NoError(flags.Set("working-dir", cwd))
	checkValue(cwd)
}

func TestStdin(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{}
	v, flags := newViper(t)

	checkValues := func(stdin bool) {
		readValue(t, v, cfg, func(cfg *config.Config) {
			as.Equal(stdin, cfg.Stdin)
		})
	}

	// default with no flag, env or config
	checkValues(false)

	// set config value and check that it has no effect
	// you are not allowed to set stdin in config
	cfg.Stdin = true

	checkValues(false)

	// env override
	t.Setenv("CONFORMIST_STDIN", "false")
	checkValues(false)

	// flag override
	as.NoError(flags.Set("stdin", "true"))
	checkValues(true)
}

func TestSampleConfigFile(t *testing.T) {
	as := require.New(t)

	v := viper.New()
	v.SetConfigFile("../test/examples/conformist.toml")
	as.NoError(v.ReadInConfig(), "failed to read config file")

	cfg, err := config.FromViper(v)
	as.NoError(err, "failed to unmarshal config from viper")

	as.NotNil(cfg)
	as.Equal([]string{"*.toml"}, cfg.Excludes)

	// python
	python, ok := cfg.FormatterConfigs["python"]
	as.True(ok, "python formatter not found")
	as.Equal("black", python.Command)
	as.Nil(python.Options)
	as.Equal([]string{"*.py"}, python.Includes)
	as.Nil(python.Excludes)

	// go
	golang, ok := cfg.FormatterConfigs["go"]
	as.True(ok, "go formatter not found")
	as.Equal("gofmt", golang.Command)
	as.Equal([]string{"-w"}, golang.Options)
	as.Equal([]string{"*.go"}, golang.Includes)
	as.Nil(golang.Excludes)

	// haskell
	haskell, ok := cfg.FormatterConfigs["haskell"]
	as.True(ok, "haskell formatter not found")
	as.Equal("ormolu", haskell.Command)
	as.Equal([]string{
		"--ghc-opt", "-XBangPatterns",
		"--ghc-opt", "-XPatternSynonyms",
		"--ghc-opt", "-XTypeApplications",
		"--mode", "inplace",
		"--check-idempotence",
	}, haskell.Options)
	as.Equal([]string{"*.hs"}, haskell.Includes)
	as.Equal([]string{"examples/haskell/"}, haskell.Excludes)

	// alejandra
	alejandra, ok := cfg.FormatterConfigs["alejandra"]
	as.True(ok, "alejandra formatter not found")
	as.Equal("alejandra", alejandra.Command)
	as.Nil(alejandra.Options)
	as.Equal([]string{"*.nix"}, alejandra.Includes)
	as.Equal([]string{"examples/nix/sources.nix"}, alejandra.Excludes)
	as.Equal(1, alejandra.Priority)

	// deadnix
	deadnix, ok := cfg.FormatterConfigs["deadnix"]
	as.True(ok, "deadnix formatter not found")
	as.Equal("deadnix", deadnix.Command)
	as.Equal([]string{"-e"}, deadnix.Options)
	as.Equal([]string{"*.nix"}, deadnix.Includes)
	as.Nil(deadnix.Excludes)
	as.Equal(2, deadnix.Priority)

	// ruby
	ruby, ok := cfg.FormatterConfigs["ruby"]
	as.True(ok, "ruby formatter not found")
	as.Equal("rufo", ruby.Command)
	as.Equal([]string{"-x"}, ruby.Options)
	as.Equal([]string{"*.rb"}, ruby.Includes)
	as.Nil(ruby.Excludes)

	// prettier
	prettier, ok := cfg.FormatterConfigs["prettier"]
	as.True(ok, "prettier formatter not found")
	as.Equal("prettier", prettier.Command)
	as.Equal([]string{"--write", "--tab-width", "4"}, prettier.Options)
	as.Equal([]string{
		"*.css",
		"*.html",
		"*.js",
		"*.json",
		"*.jsx",
		"*.md",
		"*.mdx",
		"*.scss",
		"*.ts",
		"*.yaml",
	}, prettier.Includes)
	as.Equal([]string{"CHANGELOG.md"}, prettier.Excludes)

	// rust
	rust, ok := cfg.FormatterConfigs["rust"]
	as.True(ok, "rust formatter not found")
	as.Equal("rustfmt", rust.Command)
	as.Equal([]string{"--edition", "2018"}, rust.Options)
	as.Equal([]string{"*.rs"}, rust.Includes)
	as.Nil(rust.Excludes)

	// shellcheck
	shellcheck, ok := cfg.FormatterConfigs["shellcheck"]
	as.True(ok, "shellcheck formatter not found")
	as.Equal("shellcheck", shellcheck.Command)
	as.Equal(1, shellcheck.Priority)
	as.Nil(shellcheck.Options)
	as.Equal([]string{"*.sh"}, shellcheck.Includes)
	as.Nil(shellcheck.Excludes)

	// shfmt
	shfmt, ok := cfg.FormatterConfigs["shfmt"]
	as.True(ok, "shfmt formatter not found")
	as.Equal("shfmt", shfmt.Command)
	as.Equal(2, shfmt.Priority)
	as.Equal([]string{"-i", "2", "-s", "-w"}, shfmt.Options)
	as.Equal([]string{"*.sh"}, shfmt.Includes)
	as.Nil(shfmt.Excludes)

	// opentofu
	opentofu, ok := cfg.FormatterConfigs["opentofu"]
	as.True(ok, "opentofu formatter not found")
	as.Equal("tofu", opentofu.Command)
	as.Equal([]string{"fmt"}, opentofu.Options)
	as.Equal([]string{"*.tf"}, opentofu.Includes)
	as.Nil(opentofu.Excludes)

	// missing
	foo, ok := cfg.FormatterConfigs["foo-fmt"]
	as.True(ok, "foo formatter not found")
	as.Equal("foo-fmt", foo.Command)
}

// TestLegacyEnvPrefix covers the backward-compat fallback from the former
// TREELINT_ env prefix to CONFORMIST_ (see config.NewViper). The fallback shim
// runs eagerly inside NewViper and copies via os.Setenv, so the legacy variable
// must be set before newViper is called.
func TestLegacyEnvPrefix(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	t.Run(test_ui.MakeTestCaseInfo("falls back to TREELINT_ when CONFORMIST_ is unset"), func(t *test_ui.T) {
		t.Setenv("TREELINT_ON_UNMATCHED", "debug")
		// the shim's os.Setenv copy is not tracked by t.Setenv; clean it up so
		// it does not leak into sibling tests.
		t.Cleanup(func() { _ = os.Unsetenv("CONFORMIST_ON_UNMATCHED") })

		v, _ := newViper(t)

		readValue(t, v, &config.Config{}, func(cfg *config.Config) {
			as.Equal("debug", cfg.OnUnmatched)
		})
	})

	t.Run(test_ui.MakeTestCaseInfo("CONFORMIST_ takes precedence over TREELINT_"), func(t *test_ui.T) {
		t.Setenv("TREELINT_ON_UNMATCHED", "debug")
		t.Setenv("CONFORMIST_ON_UNMATCHED", "error")

		v, _ := newViper(t)

		readValue(t, v, &config.Config{}, func(cfg *config.Config) {
			as.Equal("error", cfg.OnUnmatched)
		})
	})
}

// TestFindUpCeiling covers CONFORMIST_CEILING_DIRECTORIES, which bounds the
// upward config-discovery walk (FindUp) the way git's GIT_CEILING_DIRECTORIES
// bounds its .git search. Without it, discovery escapes into an ancestor config
// (the conformist#15 footgun); with it, the walk stops before entering the
// ceiling.
func TestFindUpCeiling(t *testing.T) {
	as := require.New(t)

	// EvalSymlinks so comparisons match eachDir's canonical handling (e.g. macOS
	// /tmp -> /private/tmp).
	root, err := filepath.EvalSymlinks(t.TempDir())
	as.NoError(err)

	deep := filepath.Join(root, "a", "b", "c")
	as.NoError(os.MkdirAll(deep, 0o755))
	// An ancestor config that upward discovery would otherwise find.
	as.NoError(os.WriteFile(filepath.Join(root, "conformist.toml"), []byte{}, 0o600))

	t.Run("escapes upward to an ancestor config without a ceiling", func(t *testing.T) {
		t.Setenv("CONFORMIST_CEILING_DIRECTORIES", "")

		path, dir, findErr := config.FindUp(deep, "conformist.toml")
		as.NoError(findErr)
		as.Equal(filepath.Join(root, "conformist.toml"), path)
		as.Equal(root, dir)
	})

	t.Run("ceiling stops the walk before the ancestor config", func(t *testing.T) {
		t.Setenv("CONFORMIST_CEILING_DIRECTORIES", filepath.Join(root, "a"))

		_, _, findErr := config.FindUp(deep, "conformist.toml")
		as.Error(findErr, "discovery must not enter the ceiling dir or above")
	})

	t.Run("the start dir is searched even when it is the ceiling", func(t *testing.T) {
		startCfg := filepath.Join(deep, "conformist.toml")
		as.NoError(os.WriteFile(startCfg, []byte{}, 0o600))
		t.Cleanup(func() { _ = os.Remove(startCfg) })

		t.Setenv("CONFORMIST_CEILING_DIRECTORIES", deep)

		path, _, findErr := config.FindUp(deep, "conformist.toml")
		as.NoError(findErr)
		as.Equal(startCfg, path)
	})

	t.Run("a leading empty entry disables symlink resolution for the rest", func(t *testing.T) {
		// `:<path>` marks <path> as already-canonical; for a non-symlink path it
		// still functions as a ceiling (GIT_CEILING_DIRECTORIES semantics).
		t.Setenv("CONFORMIST_CEILING_DIRECTORIES", string(os.PathListSeparator)+filepath.Join(root, "a"))

		_, _, findErr := config.FindUp(deep, "conformist.toml")
		as.Error(findErr)
	})
}
