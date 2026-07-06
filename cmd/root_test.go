package cmd_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/amarbel-llc/conformist/cmd"
	formatCmd "github.com/amarbel-llc/conformist/cmd/format"
	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/format"
	"github.com/amarbel-llc/conformist/stats"
	"github.com/amarbel-llc/conformist/test"
	"github.com/amarbel-llc/conformist/walk"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/charmbracelet/log"
	cp "github.com/otiai10/copy"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestOnUnmatched(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)

	test.ChangeWorkDir(t, tempDir)

	expectedPaths := []string{
		".gitignore",
		"go/go.mod",
		"haskell/haskell.cabal",
		"haskell-frontend/haskell-frontend.cabal",
		"html/scripts/.gitkeep",
		"python/requirements.txt",
		// these should not be reported, they are in the global excludes
		// - "nixpkgs.toml"
		// - "touch.toml"
		// - "conformist.toml"
		// - "rust/Cargo.toml"
		// - "haskell/conformist.toml"
	}

	// allow missing formatter
	t.Setenv("CONFORMIST_ALLOW_MISSING_FORMATTER", "true")

	checkOutput := func(level log.Level) func([]byte) {
		logPrefix := strings.ToUpper(level.String())[:4]

		regex := regexp.MustCompile(fmt.Sprintf(`^%s no formatter for path: (.*)$`, logPrefix))

		return func(out []byte) {
			var paths []string

			scanner := bufio.NewScanner(bytes.NewReader(out))
			for scanner.Scan() {
				matches := regex.FindStringSubmatch(scanner.Text())
				if len(matches) != 2 {
					continue
				}

				paths = append(paths, matches[1])
			}

			as.Equal(expectedPaths, paths)
		}
	}

	// default is INFO
	t.Run(test_ui.MakeTestCaseInfo("default"), func(t *test_ui.T) {
		conformist(t, withArgs("-v"), withNoError(t), withStderr(checkOutput(log.InfoLevel)))
	})

	// should exit with error when using fatal
	t.Run(test_ui.MakeTestCaseInfo("fatal"), func(t *test_ui.T) {
		errorFn := func(as *require.Assertions, err error) {
			as.ErrorContains(err, "no formatter for path: "+expectedPaths[0])
		}

		conformist(t, withArgs("--on-unmatched", "fatal"), withError(errorFn))

		t.Setenv("CONFORMIST_ON_UNMATCHED", "fatal")

		conformist(t, withError(errorFn))
	})

	// test other levels
	for _, levelStr := range []string{"debug", "info", "warn", "error"} {
		t.Run(test_ui.MakeTestCaseInfo(levelStr), func(t *test_ui.T) {
			level, err := log.ParseLevel(levelStr)
			as.NoError(err, "failed to parse log level: %s", level)

			conformist(
				t,
				withArgs("-vv", "--on-unmatched", levelStr),
				withNoError(t),
				withStderr(checkOutput(level)),
			)

			t.Setenv("CONFORMIST_ON_UNMATCHED", levelStr)

			conformist(
				t,
				withArgs("-vv"),
				withNoError(t),
				withStderr(checkOutput(level)),
			)
		})
	}

	t.Run(test_ui.MakeTestCaseInfo("invalid"), func(t *test_ui.T) {
		// test bad value
		errorFn := func(arg string) func(as *require.Assertions, err error) {
			return func(as *require.Assertions, err error) {
				as.ErrorContains(err, fmt.Sprintf(`invalid level: "%s"`, arg))
			}
		}

		conformist(
			t,
			withArgs("--on-unmatched", "foo"),
			withError(errorFn("foo")),
		)

		t.Setenv("CONFORMIST_ON_UNMATCHED", "bar")

		conformist(t, withError(errorFn("bar")))
	})
}

func TestQuiet(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	tempDir := test.TempExamples(t)

	test.ChangeWorkDir(t, tempDir)

	// allow missing formatter
	t.Setenv("CONFORMIST_ALLOW_MISSING_FORMATTER", "true")

	noOutput := func(out []byte) {
		as.Empty(out)
	}

	conformist(t, withArgs("-q"), withNoError(t), withStdout(noOutput), withStderr(noOutput))
	conformist(t, withArgs("--quiet"), withNoError(t), withStdout(noOutput), withStderr(noOutput))

	t.Setenv("CONFORMIST_QUIET", "true")
	conformist(t, withNoError(t), withStdout(noOutput), withStderr(noOutput))

	t.Setenv("CONFORMIST_ALLOW_MISSING_FORMATTER", "false")

	// check it doesn't suppress errors. Repair/format mode degrades on a missing
	// tool binary by default (conformist#75), so --require-tools restores the hard
	// error; quiet must still not swallow it.
	conformist(t, withArgs("--require-tools"), withError(func(as *require.Assertions, err error) {
		as.ErrorIs(err, format.ErrCommandNotFound)
		as.ErrorContains(err, "foo-fmt")
	}))
}

func TestCpuProfile(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	tempDir := test.TempExamples(t)

	test.ChangeWorkDir(t, tempDir)

	// allow missing formatter
	t.Setenv("CONFORMIST_ALLOW_MISSING_FORMATTER", "true")

	conformist(
		t,
		withArgs("--cpu-profile", "cpu.pprof"),
		withNoError(t),
	)

	as.FileExists(filepath.Join(tempDir, "cpu.pprof"))

	// test with env
	t.Setenv("CONFORMIST_CPU_PROFILE", "env.pprof")

	conformist(t, withNoError(t))

	as.FileExists(filepath.Join(tempDir, "env.pprof"))
}

func TestAllowMissingFormatter(tt *testing.T) {
	t := &test_ui.T{T: tt}
	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	test.WriteConfig(t, configPath, &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"foo-fmt": {
				Command: "foo-fmt",
			},
		},
	})

	// Repair/format mode degrades on a missing tool binary by default
	// (conformist#75): the run succeeds, skipping the unresolved formatter's lane
	// with a warning rather than aborting the whole run.
	t.Run(test_ui.MakeTestCaseInfo("default (degrades)"), func(t *test_ui.T) {
		conformist(
			t,
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   0,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)
	})

	// --require-tools restores strict failure for repair gates that must run
	// every lane.
	t.Run(test_ui.MakeTestCaseInfo("require-tools"), func(t *test_ui.T) {
		conformist(
			t,
			withArgs("--require-tools"),
			withError(func(as *require.Assertions, err error) {
				as.ErrorIs(err, format.ErrCommandNotFound)
			}),
		)
	})

	t.Run(test_ui.MakeTestCaseInfo("arg"), func(t *test_ui.T) {
		conformist(
			t,
			withArgs("--allow-missing-formatter"),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   0,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)
	})

	t.Run(test_ui.MakeTestCaseInfo("env"), func(t *test_ui.T) {
		t.Setenv("CONFORMIST_ALLOW_MISSING_FORMATTER", "true")
		conformist(t, withNoError(t))
	})
}

func TestSpecifyingFormatters(tt *testing.T) {
	t := &test_ui.T{T: tt}
	// we use the test formatter to append some whitespace
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"rust": {
				Command:  "test-fmt-append",
				Options:  []string{"   "},
				Includes: []string{"*.rs"},
			},
			"nix": {
				Command:  "test-fmt-append",
				Options:  []string{"   "},
				Includes: []string{"*.nix"},
			},
			"ruby": {
				Command:  "test-fmt-append",
				Options:  []string{"   "},
				Includes: []string{"*.rb"},
			},
		},
	}

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.WriteConfig(t, configPath, cfg)
	test.ChangeWorkDir(t, tempDir)

	t.Run(test_ui.MakeTestCaseInfo("default"), func(t *test_ui.T) {
		conformist(
			t,
			withNoError(t),
			withModtimeBump(tempDir, time.Second),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   3,
				stats.Formatted: 3,
				stats.Changed:   3,
			}),
		)
	})

	t.Run(test_ui.MakeTestCaseInfo("args"), func(t *test_ui.T) {
		conformist(
			t,
			withArgs("--formatters", "rust,nix"),
			withModtimeBump(tempDir, time.Second),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   2,
				stats.Formatted: 2,
				stats.Changed:   2,
			}),
		)

		conformist(
			t,
			withArgs("--formatters", "ruby,nix"),
			withModtimeBump(tempDir, time.Second),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   2,
				stats.Formatted: 2,
				stats.Changed:   2,
			}),
		)

		conformist(
			t,
			withArgs("--formatters", "nix"),
			withModtimeBump(tempDir, time.Second),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   1,
				stats.Formatted: 1,
				stats.Changed:   1,
			}),
		)

		// bad name
		conformist(
			t,
			withArgs("--formatters", "foo"),
			withError(func(as *require.Assertions, err error) {
				as.ErrorContains(err, "formatter foo not found in config")
			}),
		)
	})

	t.Run(test_ui.MakeTestCaseInfo("env"), func(t *test_ui.T) {
		t.Setenv("CONFORMIST_FORMATTERS", "ruby,nix")

		conformist(
			t,
			withNoError(t),
			withModtimeBump(tempDir, time.Second),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   2,
				stats.Formatted: 2,
				stats.Changed:   2,
			}),
		)

		t.Setenv("CONFORMIST_FORMATTERS", "bar,foo")

		conformist(
			t,
			withError(func(as *require.Assertions, err error) {
				as.ErrorContains(err, "formatter bar not found in config")
			}),
		)
	})

	t.Run(test_ui.MakeTestCaseInfo("bad names"), func(t *test_ui.T) {
		for _, name := range []string{"foo$", "/bar", "baz%"} {
			conformist(
				t,
				withArgs("--formatters", name),
				withError(func(as *require.Assertions, err error) {
					as.ErrorContains(err, fmt.Sprintf("formatter name %q is invalid", name))
				}),
			)

			t.Setenv("CONFORMIST_FORMATTERS", name)

			conformist(
				t,
				withError(func(as *require.Assertions, err error) {
					as.ErrorContains(err, fmt.Sprintf("formatter name %q is invalid", name))
				}),
			)

			t.Setenv("CONFORMIST_FORMATTERS", "")

			cfg.FormatterConfigs[name] = &config.Formatter{
				Command:  "echo",
				Includes: []string{"*"},
			}

			test.WriteConfig(t, configPath, cfg)

			conformist(
				t,
				withError(func(as *require.Assertions, err error) {
					as.ErrorContains(err, fmt.Sprintf("formatter name %q is invalid", name))
				}),
			)

			delete(cfg.FormatterConfigs, name)

			test.WriteConfig(t, configPath, cfg)
		}
	})
}

func TestIncludesAndExcludes(tt *testing.T) {
	t := &test_ui.T{T: tt}
	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	// test without any excludes
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:  "echo",
				Includes: []string{"*"},
			},
		},
	}

	conformist(
		t,
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   0,
		}),
	)

	// globally exclude nix files
	cfg.Excludes = []string{"*.nix"}

	conformist(
		t,
		withArgs("-c"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   32,
			stats.Formatted: 32,
			stats.Changed:   0,
		}),
	)

	// add haskell files to the global exclude
	cfg.Excludes = []string{"*.nix", "*.hs"}

	conformist(
		t,
		withArgs("-c"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   26,
			stats.Formatted: 26,
			stats.Changed:   0,
		}),
	)

	echo := cfg.FormatterConfigs["echo"]

	// remove python files from the echo formatter
	echo.Excludes = []string{"*.py"}

	conformist(
		t,
		withArgs("-c"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   24,
			stats.Formatted: 24,
			stats.Changed:   0,
		}),
	)

	// remove go files from the echo formatter via env
	t.Setenv("CONFORMIST_FORMATTER_ECHO_EXCLUDES", "*.py,*.go")

	conformist(
		t,
		withArgs("-c"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   23,
			stats.Formatted: 23,
			stats.Changed:   0,
		}),
	)

	t.Setenv("CONFORMIST_FORMATTER_ECHO_EXCLUDES", "") // reset

	// adjust the includes for echo to only include rust files
	echo.Includes = []string{"*.rs"}

	conformist(
		t,
		withArgs("-c"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   1,
			stats.Formatted: 1,
			stats.Changed:   0,
		}),
	)

	// add js files to echo formatter via env
	t.Setenv("CONFORMIST_FORMATTER_ECHO_INCLUDES", "*.rs,*.js")

	conformist(
		t,
		withArgs("-c"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   2,
			stats.Formatted: 2,
			stats.Changed:   0,
		}),
	)
}

func TestConfigFile(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	for _, name := range []string{"conformist.toml", ".conformist.toml"} {
		t.Run(test_ui.MakeTestCaseInfo(name), func(t *test_ui.T) {
			tempDir := test.TempExamples(t)

			// Work from an isolated directory holding a known set of files, to
			// avoid interference with auto walk detection from the conformist
			// repository. Its two files distinguish "walked the working dir"
			// from "walked the config file's dir" (which holds one file).
			workDir := t.TempDir()
			as.NoError(os.WriteFile(filepath.Join(workDir, "a.txt"), []byte{}, 0o600))
			as.NoError(os.WriteFile(filepath.Join(workDir, "b.txt"), []byte{}, 0o600))
			test.ChangeWorkDir(t, workDir)

			// use a config file in a different temp directory
			configPath := filepath.Join(t.TempDir(), name)

			// With an explicit, out-of-tree --config-file and no --tree-root, the
			// tree root defaults to the working directory (2 files), NOT the
			// config file's directory. See conformist#2.
			conformist(
				t,
				withConfig(configPath, &config.Config{
					FormatterConfigs: map[string]*config.Formatter{
						"echo": {
							Command:  "echo",
							Includes: []string{"*"},
						},
					},
				}),
				withArgs("--config-file", configPath),
				withNoError(t),
				withStats(t, map[stats.Type]int{
					stats.Traversed: 2,
					stats.Matched:   2,
					stats.Formatted: 2,
					stats.Changed:   0,
				}),
			)

			conformist(
				t,
				withArgs("--config-file", configPath, "--tree-root", tempDir),
				withNoError(t),
				withStats(t, map[stats.Type]int{
					stats.Traversed: 33,
					stats.Matched:   33,
					stats.Formatted: 33,
					stats.Changed:   0,
				}),
			)

			// use env variable; CONFORMIST_CONFIG is still an out-of-tree config,
			// so the tree root remains the working directory (2 files, hot cache)
			conformist(
				t,
				withEnv(map[string]string{
					// CONFORMIST_CONFIG takes precedence
					"CONFORMIST_CONFIG": configPath,
					"PRJ_ROOT":          tempDir,
				}),
				withNoError(t),
				withStats(t, map[stats.Type]int{
					stats.Traversed: 2,
					stats.Matched:   2,
					stats.Formatted: 0,
					stats.Changed:   0,
				}),
			)

			// should fallback to PRJ_ROOT
			conformist(
				t,
				withArgs("--tree-root", tempDir),
				withEnv(map[string]string{
					"PRJ_ROOT": filepath.Dir(configPath),
				}),
				withNoError(t),
				withStats(t, map[stats.Type]int{
					stats.Traversed: 33,
					stats.Matched:   33,
					stats.Formatted: 0,
					stats.Changed:   0,
				}),
			)

			// should not search upwards if using PRJ_ROOT
			configSubDir := filepath.Join(filepath.Dir(configPath), "sub")
			as.NoError(os.MkdirAll(configSubDir, 0o600))

			conformist(
				t,
				withArgs("--tree-root", tempDir),
				withEnv(map[string]string{
					"PRJ_ROOT": configSubDir,
				}),
				withError(func(as *require.Assertions, err error) {
					as.ErrorContains(err, "failed to find conformist config file")
				}),
			)
		})
	}
}

func TestCache(tt *testing.T) {
	t := &test_ui.T{T: tt}
	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	// test without any excludes
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"append": {
				Command:  "test-fmt-append",
				Options:  []string{"   "},
				Includes: []string{"*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	// first run
	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   33,
		}),
	)

	// cached run with no changes to underlying files
	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// clear cache
	conformist(
		t,
		withArgs("-c"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   33,
		}),
	)

	// cached run with no changes to underlying files
	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// bump underlying files
	conformist(
		t,
		withNoError(t),
		withModtimeBump(tempDir, time.Second),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   33,
		}),
	)

	// no cache
	conformist(
		t,
		withArgs("--no-cache"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   33,
		}),
	)

	// update the config with a failing formatter
	cfg = &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			// fails to execute
			"fail": {
				Command:  "touch",
				Options:  []string{"--bad-arg"},
				Includes: []string{"*.hs"},
			},
		},
	}
	test.WriteConfig(t, configPath, cfg)

	// test that formatting errors are not cached

	// running should match but not format anything

	conformist(
		t,
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, format.ErrFormattingFailures)
		}),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   6,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// running again should provide the same result
	conformist(
		t,
		withError(func(as *require.Assertions, err error) {
			as.ErrorIs(err, format.ErrFormattingFailures)
		}),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   6,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// let's fix the haskell config so it no longer fails
	cfg.FormatterConfigs["fail"] = &config.Formatter{
		Command:  "test-fmt-append",
		Options:  []string{"   "},
		Includes: []string{"*.hs"},
	}

	test.WriteConfig(t, configPath, cfg)

	// we should now format the haskell files
	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   6,
			stats.Formatted: 6,
			stats.Changed:   6,
		}),
	)
}

func TestChangeWorkingDirectory(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"append": {
				Command:  "test-fmt-append",
				Options:  []string{"   "},
				Includes: []string{"*"},
			},
		},
	}

	t.Run(test_ui.MakeTestCaseInfo("default"), func(t *test_ui.T) {
		// capture current cwd, so we can replace it after the test is finished
		cwd, err := os.Getwd()
		as.NoError(err)

		t.Cleanup(func() {
			//nolint:usetesting
			// return to the previous working directory
			as.NoError(os.Chdir(cwd))
		})

		tempDir := test.TempExamples(t)
		configPath := filepath.Join(tempDir, "conformist.toml")

		//nolint:usetesting
		// change to an empty temp dir and try running without specifying a working directory
		as.NoError(os.Chdir(t.TempDir()))

		conformist(
			t,
			withConfig(configPath, cfg),
			withError(func(as *require.Assertions, err error) {
				as.ErrorContains(err, "failed to find conformist config file")
			}),
		)

		//nolint:usetesting
		// now change to the examples temp directory
		as.NoError(os.Chdir(tempDir), "failed to change to temp directory")

		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
			}),
		)
	})

	execute := func(t *test_ui.T, configFile string, env bool) {
		t.Helper()
		t.Run(test_ui.MakeTestCaseInfo(configFile), func(t *test_ui.T) {
			// capture current cwd, so we can replace it after the test is finished
			cwd, err := os.Getwd()
			as.NoError(err)

			t.Cleanup(func() {
				//nolint:usetesting
				// return to the previous working directory
				as.NoError(os.Chdir(cwd))
			})

			tempDir := test.TempExamples(t)
			configPath := filepath.Join(tempDir, configFile)

			// delete conformist.toml that comes with the example folder
			as.NoError(os.Remove(filepath.Join(tempDir, "conformist.toml")))

			var args []string

			if env {
				t.Setenv("CONFORMIST_WORKING_DIR", tempDir)
			} else {
				args = []string{"-C", tempDir}
			}

			conformist(
				t,
				withArgs(args...),
				withConfig(configPath, cfg),
				withNoError(t),
				withStats(t, map[stats.Type]int{
					stats.Traversed: 33,
				}),
			)
		})
	}

	// by default, we look for a config file at ./conformist.toml or ./.conformist.toml in the current working directory
	configFiles := []string{"conformist.toml", ".conformist.toml"}

	t.Run(test_ui.MakeTestCaseInfo("arg"), func(t *test_ui.T) {
		for _, configFile := range configFiles {
			execute(t, configFile, false)
		}
	})

	t.Run(test_ui.MakeTestCaseInfo("env"), func(t *test_ui.T) {
		for _, configFile := range configFiles {
			execute(t, configFile, true)
		}
	})
}

// TestConfigFileLegacyFallback covers the backward-compat discovery of the
// former treelint.toml filename, and that conformist.toml wins when both are
// present (see the filenames list in loadConfig).
func TestConfigFileLegacyFallback(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:  "echo",
				Includes: []string{"*"},
			},
		},
	}

	t.Run(test_ui.MakeTestCaseInfo("treelint.toml discovered when conformist.toml absent"), func(t *test_ui.T) {
		tempDir := test.TempExamples(t)

		// drop the bundled conformist.toml so only the legacy name remains
		as.NoError(os.Remove(filepath.Join(tempDir, "conformist.toml")))

		test.ChangeWorkDir(t, tempDir)

		conformist(
			t,
			withConfig(filepath.Join(tempDir, "treelint.toml"), cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
			}),
		)
	})

	t.Run(test_ui.MakeTestCaseInfo("conformist.toml preferred over treelint.toml"), func(t *test_ui.T) {
		tempDir := test.TempExamples(t)

		// a legacy treelint.toml whose formatter command does not exist: if it
		// were chosen over conformist.toml the run would fail.
		test.WriteConfig(t, filepath.Join(tempDir, "treelint.toml"), &config.Config{
			FormatterConfigs: map[string]*config.Formatter{
				"missing": {
					Command:  "conformist-no-such-command",
					Includes: []string{"*"},
				},
			},
		})

		test.ChangeWorkDir(t, tempDir)

		// conformist.toml (written here) uses the valid echo formatter; a
		// no-error run proves treelint.toml's broken formatter was not loaded.
		// 34 = the 33 bundled example files + the extra treelint.toml we wrote.
		conformist(
			t,
			withConfig(filepath.Join(tempDir, "conformist.toml"), cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 34,
			}),
		)
	})
}

func TestFailOnChange(tt *testing.T) {
	t := &test_ui.T{T: tt}
	t.Run(test_ui.MakeTestCaseInfo("change size"), func(t *test_ui.T) {
		tempDir := test.TempExamples(t)
		configPath := filepath.Join(tempDir, "conformist.toml")

		test.ChangeWorkDir(t, tempDir)

		cfg := &config.Config{
			FormatterConfigs: map[string]*config.Formatter{
				"append": {
					// test-fmt-append is a helper defined in nix/packages/conformist/formatters.nix which lets us append
					// an arbitrary value to a list of files
					Command:  "test-fmt-append",
					Options:  []string{"hello"},
					Includes: []string{"rust/*"},
				},
			},
		}

		// running with a cold cache, we should see the rust files being formatted, resulting in changes, which should
		// trigger an error
		conformist(
			t,
			withArgs("--fail-on-change"),
			withConfig(configPath, cfg),
			withError(func(as *require.Assertions, err error) {
				as.ErrorIs(err, formatCmd.ErrFailOnChange)
			}),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   2,
				stats.Formatted: 2,
				stats.Changed:   2,
			}),
		)

		// running with a hot cache, we should see matches for the rust files, but no attempt to format them as the
		// underlying files have not changed since we last ran
		conformist(
			t,
			withArgs("--fail-on-change"),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   2,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)
	})

	t.Run(test_ui.MakeTestCaseInfo("change modtime"), func(t *test_ui.T) {
		tempDir := test.TempExamples(t)
		configPath := filepath.Join(tempDir, "conformist.toml")

		test.ChangeWorkDir(t, tempDir)

		dateFormat := "2006 01 02 15:04.05"
		replacer := strings.NewReplacer(" ", "", ":", "")

		formatTime := func(t time.Time) string {
			// go date formats are stupid
			return replacer.Replace(t.Format(dateFormat))
		}

		// running with a cold cache, we should see the haskell files being formatted, resulting in changes, which should
		// trigger an error
		conformist(
			t,
			withArgs("--fail-on-change"),
			withConfigFunc(configPath, func() *config.Config {
				// new mod time is in the next second
				modTime := time.Now().Truncate(time.Second).Add(time.Second)

				return &config.Config{
					FormatterConfigs: map[string]*config.Formatter{
						"append": {
							// test-fmt-modtime is a helper defined in nix/packages/conformist/formatters.nix which lets us set
							// a file's modtime to an arbitrary date.
							// in this case, we move it forward more than a second so that our second level modtime comparison
							// will detect it as a change.
							Command:  "test-fmt-modtime",
							Options:  []string{formatTime(modTime)},
							Includes: []string{"haskell/*"},
						},
					},
				}
			}),
			withError(func(as *require.Assertions, err error) {
				as.ErrorIs(err, formatCmd.ErrFailOnChange)
			}),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   7,
				stats.Formatted: 7,
				stats.Changed:   7,
			}),
		)

		// running with a hot cache, we should see matches for the haskell files, but no attempt to format them as the
		// underlying files have not changed since we last ran
		conformist(
			t,
			withArgs("--fail-on-change"),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   7,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)
	})
}

func TestCacheBusting(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	t.Run(test_ui.MakeTestCaseInfo("formatter_change_config"), func(t *test_ui.T) {
		tempDir := test.TempExamples(t)
		configPath := filepath.Join(tempDir, "conformist.toml")

		test.ChangeWorkDir(t, tempDir)

		// basic config
		cfg := &config.Config{
			FormatterConfigs: map[string]*config.Formatter{
				"python": {
					Command:  "echo", // this is non-destructive, will match but cause no changes
					Includes: []string{"*.py"},
				},
				"haskell": {
					Command:  "test-fmt-append",
					Options:  []string{"   "},
					Includes: []string{"*.hs"},
				},
			},
		}

		// initial run
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   8,
				stats.Formatted: 8,
				stats.Changed:   6,
			}))

		// change formatter options
		cfg.FormatterConfigs["haskell"].Options = []string{""}

		// cache entries for haskell files should be invalidated
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   8,
				stats.Formatted: 6,
				stats.Changed:   6,
			}))

		// run again, nothing should be formatted
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   8,
				stats.Formatted: 0,
				stats.Changed:   0,
			}))

		// change the formatter's command
		cfg.FormatterConfigs["haskell"].Command = "echo"

		// cache entries for haskell files should be invalidated
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   8,
				stats.Formatted: 6,
				stats.Changed:   0, // echo doesn't affect the files so no changes expected
			}))

		// run again, nothing should be formatted
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   8,
				stats.Formatted: 0,
				stats.Changed:   0,
			}))

		// change the formatters includes
		cfg.FormatterConfigs["haskell"].Includes = []string{"haskell/*.hs"}

		// we should match on fewer files, but no formatting should occur as includes are not part of the formatting
		// signature
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   6,
				stats.Formatted: 0,
				stats.Changed:   0,
			}))

		// change the formatters excludes
		cfg.FormatterConfigs["haskell"].Excludes = []string{"haskell/Foo.hs"}

		// we should match on fewer files, but no formatting should occur as excludes are not part of the formatting
		// signature
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   5,
				stats.Formatted: 0,
				stats.Changed:   0,
			}))
	})

	t.Run(test_ui.MakeTestCaseInfo("formatter_change_binary"), func(t *test_ui.T) {
		tempDir := test.TempExamples(t)
		configPath := filepath.Join(tempDir, "conformist.toml")

		test.ChangeWorkDir(t, tempDir)

		// find test-fmt-append in PATH
		sourcePath, err := exec.LookPath("test-fmt-append")
		as.NoError(err, "failed to find test-fmt-append in PATH")

		// copy it into the temp dir so we can mess with its size and modtime
		binPath := filepath.Join(tempDir, "bin")
		as.NoError(os.Mkdir(binPath, 0o755))

		scriptPath := filepath.Join(binPath, "test-fmt-append")
		as.NoError(cp.Copy(sourcePath, scriptPath, cp.Options{AddPermission: 0o755}))

		// prepend our test bin directory to PATH
		t.Setenv("PATH", binPath+":"+os.Getenv("PATH"))

		// basic config
		cfg := &config.Config{
			FormatterConfigs: map[string]*config.Formatter{
				"python": {
					Command:  "echo", // this is non-destructive, will match but cause no changes
					Includes: []string{"*.py"},
				},
				"rust": {
					Command:  "test-fmt-append",
					Options:  []string{"   "},
					Includes: []string{"*.rs"},
				},
			},
		}

		// initial run
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 34,
				stats.Matched:   3,
				stats.Formatted: 3,
				stats.Changed:   1,
			}))

		// tweak mod time of rust formatter
		newTime := time.Now().Add(-time.Minute)
		as.NoError(os.Chtimes(scriptPath, newTime, newTime))

		// cache entries for rust files should be invalidated
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 34,
				stats.Matched:   3,
				stats.Formatted: 1,
				stats.Changed:   1,
			}))

		// running again with a hot cache, we should see nothing be formatted
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 34,
				stats.Matched:   3,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)

		// tweak the size of rust formatter
		formatter, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_APPEND, 0o755)
		as.NoError(err, "failed to open rust formatter")

		_, err = formatter.WriteString(" ") // add some whitespace
		as.NoError(err, "failed to append to rust formatter")
		as.NoError(formatter.Close(), "failed to close rust formatter")

		// cache entries for rust files should be invalidated
		conformist(t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 34,
				stats.Matched:   3,
				stats.Formatted: 1,
				stats.Changed:   1,
			}))

		// running again with a hot cache, we should see nothing be formatted
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 34,
				stats.Matched:   3,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)
	})

	t.Run(test_ui.MakeTestCaseInfo("formatter_add_remove"), func(t *test_ui.T) {
		tempDir := test.TempExamples(t)
		configPath := filepath.Join(tempDir, "conformist.toml")

		test.ChangeWorkDir(t, tempDir)

		cfg := &config.Config{
			FormatterConfigs: map[string]*config.Formatter{
				"python": {
					Command:  "test-fmt-append",
					Options:  []string{"   "},
					Includes: []string{"*.py"},
				},
			},
		}

		// initial run
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   2,
				stats.Formatted: 2,
				stats.Changed:   2,
			}),
		)

		// cached run
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   2,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)

		// add a formatter
		cfg.FormatterConfigs["rust"] = &config.Formatter{
			Command:  "test-fmt-append",
			Options:  []string{"   "},
			Includes: []string{"*.rs"},
		}

		// only the rust files should be formatted
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   3,
				stats.Formatted: 1,
				stats.Changed:   1,
			}),
		)

		// let's add a second python formatter
		cfg.FormatterConfigs["python_secondary"] = &config.Formatter{
			Command:  "test-fmt-append",
			Options:  []string{" "},
			Includes: []string{"*.py"},
		}

		// python files should be formatted as their pipeline has changed
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   3,
				stats.Formatted: 2,
				stats.Changed:   2,
			}),
		)

		// cached run
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   3,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)

		// change ordering within a pipeline
		cfg.FormatterConfigs["python"].Priority = 2
		cfg.FormatterConfigs["python_secondary"].Priority = 1

		// python files should be formatted as their pipeline has changed
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   3,
				stats.Formatted: 2,
				stats.Changed:   2,
			}),
		)

		// cached run
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   3,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)

		// remove secondary python formatter
		delete(cfg.FormatterConfigs, "python_secondary")

		// python files should be formatted as their pipeline has changed
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   3,
				stats.Formatted: 2,
				stats.Changed:   2,
			}),
		)

		// cached run
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   3,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)

		// remove the rust formatter
		delete(cfg.FormatterConfigs, "rust")

		// only python files should match, but no formatting should occur as not formatting signatures have been
		// affected
		conformist(
			t,
			withConfig(configPath, cfg),
			withNoError(t),
			withStats(t, map[stats.Type]int{
				stats.Traversed: 33,
				stats.Matched:   2,
				stats.Formatted: 0,
				stats.Changed:   0,
			}),
		)
	})
}

func TestGit(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "/conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	// basic config
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:  "echo", // will not generate any underlying changes in the file
				Includes: []string{"*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	// init a git repo
	gitCmd := exec.CommandContext(t.Context(), "git", "init")
	as.NoError(gitCmd.Run(), "failed to init git repository")

	// run before adding anything to the index
	// we should pick up untracked files since we use `git ls-files -o`
	conformist(
		t,
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   0,
		}),
	)

	// add everything to the index
	gitCmd = exec.CommandContext(t.Context(), "git", "add", ".")
	as.NoError(gitCmd.Run(), "failed to add everything to the index")

	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// create a file which should be in .gitignore
	f, err := os.CreateTemp(tempDir, "test-*.txt")
	as.NoError(err, "failed to create temp file")

	t.Cleanup(func() {
		_ = f.Close()
	})

	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// remove python directory
	as.NoError(os.RemoveAll(filepath.Join(tempDir, "python")), "failed to remove python directory")

	// we should traverse and match against fewer files, but no formatting should occur as no formatting signatures
	// are impacted
	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 30,
			stats.Matched:   30,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// remove nixpkgs.toml from the filesystem but leave it in the index
	as.NoError(os.Remove(filepath.Join(tempDir, "nixpkgs.toml")))

	// walk with filesystem instead of with git
	// the .git folder contains 50 additional files
	// when added to the 30 we started with (34 minus nixpkgs.toml which we removed from the filesystem), we should
	// traverse 82 files.
	conformist(
		t,
		withArgs("--walk", "filesystem"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 82,
			stats.Matched:   82,
			stats.Formatted: 53, // the echo formatter should only be applied to the new files
			stats.Changed:   0,
		}),
	)

	// format specific sub paths
	// we should traverse and match against those files, but without any underlying change to their files or their
	// formatting config, we will not format them

	conformist(
		t,
		withArgs("go"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 2,
			stats.Matched:   2,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	conformist(
		t,
		withArgs("go", "haskell"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 9,
			stats.Matched:   9,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	conformist(
		t,
		withArgs("-C", tempDir, "go", "haskell", "ruby"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 10,
			stats.Matched:   10,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// try with a bad path
	conformist(
		t,
		withArgs("-C", tempDir, "haskell", "foo"),
		withConfig(configPath, cfg),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "foo not found")
		}),
	)

	// try with a path not in the git index
	_, err = os.Create(filepath.Join(tempDir, "foo.txt"))
	as.NoError(err)

	conformist(
		t,
		withArgs("haskell", "foo.txt", "-vv"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 8,
			stats.Matched:   8,
			stats.Formatted: 1, // we only format foo.txt, which is new to the cache
			stats.Changed:   0,
		}),
	)

	conformist(
		t,
		withArgs("go", "foo.txt"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 3,
			stats.Matched:   3,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	conformist(
		t,
		withArgs("foo.txt"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 1,
			stats.Matched:   1,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)
}

func TestJujutsu(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	test.SetenvXdgConfigDir(t)
	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "/conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	// basic config — explicitly set walk to "jujutsu" because jj git init creates
	// a .git/ directory, and the Auto walker would pick Git first
	cfg := &config.Config{
		Walk: "jujutsu",
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:  "echo", // will not generate any underlying changes in the file
				Includes: []string{"*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	// init a jujutsu repo
	jjCmd := exec.CommandContext(t.Context(), "jj", "git", "init")
	as.NoError(jjCmd.Run(), "failed to init jujutsu repository")

	// run conformist before adding anything to the jj index
	// Jujutsu depends on updating the index with a `jj` command. So, until we do
	// that, the conformist should return nothing, since the walker is executed with
	// `--ignore-working-copy` which does not update the index.
	conformist(
		t,
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 0,
			stats.Matched:   0,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// update jujutsu's index
	jjCmd = exec.CommandContext(t.Context(), "jj")
	as.NoError(jjCmd.Run(), "failed to update the index")

	// This is our first pass, since previously the files were not in the index. This should format all files.
	conformist(
		t,
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   0,
		}),
	)

	// create a file which should be in .gitignore
	f, err := os.CreateTemp(tempDir, "test-*.txt")
	as.NoError(err, "failed to create temp file")

	// update jujutsu's index
	jjCmd = exec.CommandContext(t.Context(), "jj")
	as.NoError(jjCmd.Run(), "failed to update the index")

	t.Cleanup(func() {
		_ = f.Close()
	})

	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// remove python directory
	as.NoError(os.RemoveAll(filepath.Join(tempDir, "python")), "failed to remove python directory")

	// update jujutsu's index
	jjCmd = exec.CommandContext(t.Context(), "jj")
	as.NoError(jjCmd.Run(), "failed to update the index")

	// we should traverse and match against fewer files, but no formatting should occur as no formatting signatures
	// are impacted
	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 30,
			stats.Matched:   30,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// remove nixpkgs.toml from the filesystem but leave it in the index
	as.NoError(os.Remove(filepath.Join(tempDir, "nixpkgs.toml")))

	// walk with filesystem instead of with jujutsu
	// the .jj and .git folders contain additional internal files (count varies
	// by jj version); total = 29 example files + jj/git internal files
	conformist(
		t,
		withArgs("--walk", "filesystem"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 137,
			stats.Matched:   137,
			stats.Formatted: 108,
			stats.Changed:   0,
		}),
	)

	// format specific sub paths
	// we should traverse and match against those files, but without any underlying change to their files or their
	// formatting config, we will not format them

	conformist(
		t,
		withArgs("go"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 2,
			stats.Matched:   2,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	conformist(
		t,
		withArgs("go", "haskell"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 9,
			stats.Matched:   9,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	conformist(
		t,
		withArgs("-C", tempDir, "go", "haskell", "ruby"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 10,
			stats.Matched:   10,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// try with a bad path
	conformist(
		t,
		withArgs("-C", tempDir, "haskell", "foo"),
		withConfig(configPath, cfg),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "foo not found")
		}),
	)

	// try with a path not in the jj index
	_, err = os.Create(filepath.Join(tempDir, "foo.txt"))
	as.NoError(err)

	// update jujutsu's index
	jjCmd = exec.CommandContext(t.Context(), "jj")
	as.NoError(jjCmd.Run(), "failed to update the index")

	conformist(
		t,
		withArgs("haskell", "foo.txt", "-vv"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 8,
			stats.Matched:   8,
			stats.Formatted: 1, // we only format foo.txt, which is new to the cache
			stats.Changed:   0,
		}),
	)

	conformist(
		t,
		withArgs("go", "foo.txt"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 3,
			stats.Matched:   3,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	conformist(
		t,
		withArgs("foo.txt"),
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 1,
			stats.Matched:   1,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)
}

func TestTreeRootCmd(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "/conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	// basic config
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:  "echo", // will not generate any underlying changes in the file
				Includes: []string{"*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	// construct a tree root command with some error logging and dumping output on stdout
	treeRootCmd := func(output string) string {
		return fmt.Sprintf("bash -c '>&2 echo -e \"some error text\nsome more error text\" && echo %s'", output)
	}

	// helper for checking the contents of stderr matches our expected debug output
	checkStderr := func(buf []byte) {
		output := string(buf)
		as.Contains(output, "DEBU tree-root-cmd | stderr: some error text\n")
		as.Contains(output, "DEBU tree-root-cmd | stderr: some more error text\n")
	}

	// run conformist with DEBUG logging enabled and with tree root cmd being the root of the temp directory
	conformist(
		t,
		withArgs("-vv", "--tree-root-cmd", treeRootCmd(tempDir)),
		withNoError(t),
		withStderr(checkStderr),
		withConfig(configPath, cfg),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   0,
		}),
	)

	// run from a subdirectory, mixing things up by specifying the command via an env variable
	conformist(
		t,
		withArgs("-vv"),
		withEnv(map[string]string{
			"CONFORMIST_TREE_ROOT_CMD": treeRootCmd(filepath.Join(tempDir, "go")),
		}),
		withNoError(t),
		withStderr(checkStderr),
		withConfig(configPath, cfg),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 2,
			stats.Matched:   2,
			stats.Formatted: 2,
			stats.Changed:   0,
		}),
	)

	// run from a subdirectory, mixing things up by specifying the command via config
	cfg.TreeRootCmd = treeRootCmd(filepath.Join(tempDir, "haskell"))

	conformist(
		t,
		withArgs("-vv"),
		withNoError(t),
		withStderr(checkStderr),
		withConfig(configPath, cfg),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 7,
			stats.Matched:   7,
			stats.Formatted: 7,
			stats.Changed:   0,
		}),
	)

	// run with a long-running command (2 seconds or more)
	conformist(
		t,
		withArgs(
			"-vv",
			"--tree-root-cmd", fmt.Sprintf(
				"bash -c 'sleep 2 && echo %s'",
				tempDir,
			),
		),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "tree-root-cmd was killed after taking more than 2s to execute")
		}),
		withConfig(configPath, cfg),
	)

	// run with a command that outputs multiple lines
	conformist(
		t,
		withArgs(
			"--tree-root-cmd", fmt.Sprintf(
				"bash -c 'echo %s && echo %s'",
				tempDir, tempDir,
			),
		),
		withStderr(func(buf []byte) {
			as.Contains(string(buf), fmt.Sprintf("ERRO tree-root-cmd | stdout: \n%s\n%s\n", tempDir, tempDir))
		}),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "tree-root-cmd cannot output multiple lines")
		}),
		withConfig(configPath, cfg),
	)
}

func TestTreeRootExclusivity(tt *testing.T) {
	t := &test_ui.T{T: tt}
	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "/conformist.toml")

	formatterConfigs := map[string]*config.Formatter{
		"echo": {
			Command:  "echo", // will not generate any underlying changes in the file
			Includes: []string{"*"},
		},
	}

	test.ChangeWorkDir(t, tempDir)

	assertExclusiveFlag := func(as *require.Assertions, err error) {
		as.ErrorContains(
			err,
			"if any flags in the group [tree-root tree-root-cmd tree-root-file] are set none of the others can be;",
		)
	}

	assertExclusiveConfig := func(as *require.Assertions, err error) {
		as.ErrorContains(
			err,
			"at most one of tree-root, tree-root-cmd or tree-root-file can be specified",
		)
	}

	envValues := map[string][]string{
		"tree-root":      {"CONFORMIST_TREE_ROOT", "bar"},
		"tree-root-cmd":  {"CONFORMIST_TREE_ROOT_CMD", "echo /foo/bar"},
		"tree-root-file": {"CONFORMIST_TREE_ROOT_FILE", ".git/config"},
	}

	flagValues := map[string][]string{
		"tree-root":      {"--tree-root", "bar"},
		"tree-root-cmd":  {"--tree-root-cmd", "'echo /foo/bar'"},
		"tree-root-file": {"--tree-root-file", ".git/config"},
	}

	configValues := map[string]func(*config.Config){
		"tree-root": func(cfg *config.Config) {
			cfg.TreeRoot = "bar"
		},
		"tree-root-cmd": func(cfg *config.Config) {
			cfg.TreeRootCmd = "'echo /foo/bar'"
		},
		"tree-root-file": func(cfg *config.Config) {
			cfg.TreeRootFile = ".git/config"
		},
	}

	invalidCombinations := [][]string{
		{"tree-root", "tree-root-cmd"},
		{"tree-root", "tree-root-file"},
		{"tree-root-cmd", "tree-root-file"},
		{"tree-root", "tree-root-cmd", "tree-root-file"},
	}

	// TODO we should also test mixing the various methods in the same test e.g. env variable and config value
	// Given that ultimately everything is being reduced into the config object after parsing from viper, I'm fairly
	// confident if these tests all pass then the mixed methods should yield the same result.

	// for each set of invalid args, test them with flags, environment variables, and config entries.
	for _, combination := range invalidCombinations {
		// test flags
		var args []string
		for _, key := range combination {
			args = append(args, flagValues[key]...)
		}

		conformist(
			t,
			withArgs(args...),
			withError(assertExclusiveFlag),
		)

		// test env variables
		env := make(map[string]string)

		for _, key := range combination {
			entry := envValues[key]
			env[entry[0]] = entry[1]
		}

		conformist(
			t,
			withEnv(env),
			withError(assertExclusiveConfig),
		)

		// test config
		cfg := &config.Config{
			FormatterConfigs: formatterConfigs,
		}

		for _, key := range combination {
			entry := configValues[key]
			entry(cfg)
		}

		conformist(
			t,
			withConfig(configPath, cfg),
			withError(assertExclusiveConfig),
		)
	}
}

func TestPathsArg(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	// capture current cwd, so we can replace it after the test is finished
	cwd, err := os.Getwd()
	as.NoError(err)

	t.Cleanup(func() {
		//nolint:usetesting
		// return to the previous working directory
		as.NoError(os.Chdir(cwd))
	})

	// create a project root under a temp dir to verify behaviour with files inside the temp dir, but outside the
	// project root
	tempDir := t.TempDir()
	treeRoot := filepath.Join(tempDir, "tree-root")

	test.TempExamplesInDir(t, treeRoot)

	configPath := filepath.Join(treeRoot, "/conformist.toml")

	// create a file outside the treeRoot
	externalFile, err := os.Create(filepath.Join(tempDir, "outside_tree.go"))
	as.NoError(err)

	//nolint:usetesting
	// change working directory to project root
	as.NoError(os.Chdir(treeRoot))

	// basic config
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:  "echo",
				Includes: []string{"*"},
			},
		},
	}

	test.WriteConfig(t, configPath, cfg)

	// without any path args
	conformist(
		t,
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   0,
		}),
	)

	// specify some explicit paths
	conformist(
		t,
		withArgs("rust/src/main.rs", "haskell/Nested/Foo.hs"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 2,
			stats.Matched:   2,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// specify an absolute path
	absoluteInternalPath, err := filepath.Abs("rust/src/main.rs")
	as.NoError(err)

	conformist(
		t,
		withArgs(absoluteInternalPath),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 1,
			stats.Matched:   1,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
	)

	// specify a bad path
	conformist(
		t,
		withArgs("rust/src/main.rs", "haskell/Nested/Bar.hs"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "Bar.hs not found")
		}),
	)

	// specify an absolute path outside the tree root
	absoluteExternalPath, err := filepath.Abs(externalFile.Name())
	as.NoError(err)
	as.FileExists(absoluteExternalPath, "external file must exist")

	conformist(
		t,
		withArgs(absoluteExternalPath),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, fmt.Sprintf("path %s not inside the tree root", absoluteExternalPath))
		}),
	)

	// specify a relative path outside the tree root
	relativeExternalPath := "../outside_tree.go"
	as.FileExists(relativeExternalPath, "external file must exist")

	conformist(
		t,
		withArgs(relativeExternalPath),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, fmt.Sprintf("path %s not inside the tree root", relativeExternalPath))
		}),
	)
}

func TestStdin(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	tempDir := test.TempExamples(t)

	test.ChangeWorkDir(t, tempDir)

	// capture current stdin and replace it on test cleanup
	prevStdIn := os.Stdin

	t.Cleanup(func() {
		os.Stdin = prevStdIn
	})

	// omit the required filename parameter
	contents := `{ foo, ... }: "hello"`
	os.Stdin = test.TempFile(t, "", "stdin", &contents)

	// for convenience so we don't have to specify it in the args
	t.Setenv("CONFORMIST_ALLOW_MISSING_FORMATTER", "true")

	// we get an error about the missing filename parameter.
	conformist(
		t,
		withArgs("--stdin"),
		withError(func(as *require.Assertions, err error) {
			as.EqualError(err, "exactly one path should be specified when using the --stdin flag")
		}),
		withStderr(func(out []byte) {
			as.Equal("Error: exactly one path should be specified when using the --stdin flag\n", string(out))
		}),
	)

	// now pass along the filename parameter
	os.Stdin = test.TempFile(t, "", "stdin", &contents)

	conformist(
		t,
		withArgs("--stdin", "test.nix"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 1,
			stats.Matched:   1,
			stats.Formatted: 1,
			stats.Changed:   1,
		}),
		withStdout(func(out []byte) {
			as.Equal(`{ ...}: "hello"
`, string(out))
		}),
	)

	// the nix formatters should have reduced the example to the following

	// try a file that's outside of the project root
	os.Stdin = test.TempFile(t, "", "stdin", &contents)

	conformist(
		t,
		withArgs("--stdin", "../test.nix"),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "path ../test.nix not inside the tree root "+tempDir)
		}),
		withStderr(func(out []byte) {
			as.Contains(string(out), "Error: failed to create walker: path ../test.nix not inside the tree root")
		}),
	)

	// try some markdown instead
	contents = `
| col1 | col2 |
| ---- | ---- |
| nice | fits |
| oh no! | it's ugly |
`
	os.Stdin = test.TempFile(t, "", "stdin", &contents)

	conformist(
		t,
		withArgs("--stdin", "test.md"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 1,
			stats.Matched:   1,
			stats.Formatted: 1,
			stats.Changed:   1,
		}),
		withStdout(func(out []byte) {
			as.Equal(`| col1   | col2      |
| ------ | --------- |
| nice   | fits      |
| oh no! | it's ugly |
`, string(out))
		}),
	)

	// try with a justfile and a path which doesn't exist within the project root.
	// No leading blank line in the input: just --fmt (an --unstable feature)
	// preserves leading blanks as of just 1.51.0, so asserting it gets stripped
	// would couple this test to a specific just version. See issue discussion.
	contents = `# print this message
help:
        just --list --list-submodules --unsorted
`
	os.Stdin = test.TempFile(t, "", "stdin", &contents)

	conformist(
		t,
		withArgs("--stdin", "foo/justfile"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 1,
			stats.Matched:   1,
			stats.Formatted: 1,
			stats.Changed:   1,
		}),
		withStdout(func(out []byte) {
			as.Equal(`# print this message
help:
    just --list --list-submodules --unsorted
`, string(out))
		}),
	)
}

func TestDeterministicOrderingInPipeline(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := tempDir + "/conformist.toml"

	test.ChangeWorkDir(t, tempDir)

	test.WriteConfig(t, configPath, &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			// a and b have no priority set, which means they default to 0 and should execute first
			// a and b should execute in lexicographical order
			// c should execute first since it has a priority of 1
			"fmt-a": {
				Command:  "test-fmt-append",
				Options:  []string{"fmt-a"},
				Includes: []string{"*.py"},
			},
			"fmt-b": {
				Command:  "test-fmt-append",
				Options:  []string{"fmt-b"},
				Includes: []string{"*.py"},
			},
			"fmt-c": {
				Command:  "test-fmt-append",
				Options:  []string{"fmt-c"},
				Includes: []string{"*.py"},
				Priority: 1,
			},
		},
	})

	conformist(t, withNoError(t))

	matcher := regexp.MustCompile("^fmt-(.*)")

	// check each affected file for the sequence of test statements which should be prepended to the end
	sequence := []string{"fmt-a", "fmt-b", "fmt-c"}
	paths := []string{"python/main.py", "python/virtualenv_proxy.py"}

	for _, p := range paths {
		file, err := os.Open(filepath.Join(tempDir, p))
		as.NoError(err)

		scanner := bufio.NewScanner(file)
		idx := 0

		for scanner.Scan() {
			line := scanner.Text()

			matches := matcher.FindAllString(line, -1)
			if len(matches) != 1 {
				continue
			}

			as.Equal(sequence[idx], matches[0])

			idx++
		}
	}

	// test with a file that is in global excludes
	// all toml files are globally excluded in the test config
	badToml := `
	foo = "bla"
		bar = [ "bla" ]
	adsfadf;
`
	os.Stdin = test.TempFile(t, "", "stdin", &badToml)

	conformist(
		t,
		withArgs("--stdin", "conformist.toml"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 1,
			stats.Matched:   0,
			stats.Formatted: 0,
			stats.Changed:   0,
		}),
		withStdout(func(out []byte) {
			// it should not have been modified, simply emitted again
			as.Equal(badToml, string(out))
		}),
	)
}

func TestRunInSubdir(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	// Run the same test for each walk type
	for _, walkType := range walk.TypeValues() {
		t.Run(test_ui.MakeTestCaseInfo(walkType.String()), func(t *test_ui.T) {
			tempDir := test.TempExamples(t)
			configPath := filepath.Join(tempDir, "/conformist.toml")

			test.ChangeWorkDir(t, tempDir)

			// set the walk type via environment variable
			t.Setenv("CONFORMIST_WALK_TYPE", walkType.String())

			// if we are testing git walking, init a git repo before continuing
			if walkType == walk.Git {
				// init a git repo
				gitCmd := exec.CommandContext(t.Context(), "git", "init")
				gitCmd.Dir = tempDir
				as.NoError(gitCmd.Run(), "failed to init git repository")

				// add everything to the index
				gitCmd = exec.CommandContext(t.Context(), "git", "add", ".")
				gitCmd.Dir = tempDir
				as.NoError(gitCmd.Run(), "failed to add everything to the index")
			}

			// test that formatters are resolved relative to the conformist root
			echoPath, err := exec.LookPath("echo")
			as.NoError(err)

			echoRel := path.Join(tempDir, "echo")

			err = os.Symlink(echoPath, echoRel)
			as.NoError(err)

			//nolint:usetesting
			// change working directory to subdirectory
			as.NoError(os.Chdir(filepath.Join(tempDir, "go")))

			// basic config
			cfg := &config.Config{
				FormatterConfigs: map[string]*config.Formatter{
					"echo": {
						Command:  "./echo",
						Includes: []string{"*"},
					},
				},
			}

			test.WriteConfig(t, configPath, cfg)

			// without any path args, should reformat the whole tree
			conformist(
				t,
				withNoError(t),
				withStats(t, map[stats.Type]int{
					stats.Traversed: 33,
					stats.Matched:   33,
					stats.Formatted: 33,
					stats.Changed:   0,
				}),
			)

			// specify some explicit paths, relative to the tree root
			// this should not work, as we're in a subdirectory
			conformist(
				t,
				withArgs("-c", "go/main.go", "haskell/Nested/Foo.hs"),
				withError(func(as *require.Assertions, err error) {
					as.ErrorContains(err, "go/main.go not found")
				}),
			)

			// specify some explicit paths, relative to the current directory
			conformist(
				t,
				withArgs("-c", "main.go", "../haskell/Nested/Foo.hs"),
				withNoError(t),
				withStats(t, map[stats.Type]int{
					stats.Traversed: 2,
					stats.Matched:   2,
					stats.Formatted: 2,
					stats.Changed:   0,
				}),
			)
		})
	}
}

// Check that supplying paths on the command-line works when an element of the
// project root is a symlink.
//
// Regression test for #578.
//
// See: https://github.com/numtide/treefmt/issues/578
func TestProjectRootIsSymlink(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := t.TempDir()
	realRoot := filepath.Join(tempDir, "/real-root")
	test.TempExamplesInDir(t, realRoot)

	symlinkRoot := filepath.Join(tempDir, "/project-root")
	err := os.Symlink(realRoot, symlinkRoot)
	as.NoError(err)

	test.ChangeWorkDir(t, symlinkRoot)

	// basic config
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:  "echo",
				Includes: []string{"*"},
			},
		},
	}

	configPath := filepath.Join(symlinkRoot, "/conformist.toml")
	test.WriteConfig(t, configPath, cfg)

	// Verify we can format a specific file.
	conformist(
		t,
		withArgs("-c", "go/main.go"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 1,
			stats.Matched:   1,
			stats.Formatted: 1,
			stats.Changed:   0,
		}),
	)

	// Verify we can format a specific directory that is a symlink.
	conformist(
		t,
		withArgs("-c", "symlink-to-yaml-dir"),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 1,
			stats.Matched:   1,
			stats.Formatted: 1,
			stats.Changed:   0,
		}),
	)

	// Verify we can format the current directory (which is a symlink!).
	conformist(
		t,
		withArgs("-c", "."),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   0,
		}),
	)
}

func TestConcurrentInvocation(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "/conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:  "echo",
				Includes: []string{"*"},
			},
			"slow": {
				Command: "test-fmt-delayed-append",
				// connect timeout for the db is 1 second
				// wait 2 seconds before appending ' ' to each provided path
				Options:  []string{"2", " "},
				Includes: []string{"*"},
			},
		},
	}

	eg := errgroup.Group{}

	// concurrent invocation with one slow instance and one not

	eg.Go(func() error {
		conformist(
			t,
			withArgs("--formatters", "slow"),
			withConfig(configPath, cfg),
			withNoError(t),
		)

		return nil
	})

	time.Sleep(500 * time.Millisecond)

	conformist(
		t,
		withArgs("--formatters", "echo"),
		withConfig(configPath, cfg),
		withError(func(as *require.Assertions, err error) {
			as.ErrorContains(err, "failed to open cache")
		}),
	)

	as.NoError(eg.Wait())

	// concurrent invocation with one slow instance and one configured to clear the cache

	eg.Go(func() error {
		conformist(
			t,
			withArgs("--formatters", "slow"),
			withConfig(configPath, cfg),
			withNoError(t),
		)

		return nil
	})

	time.Sleep(500 * time.Millisecond)

	conformist(
		t,
		withArgs("-c", "--formatters", "echo"),
		withConfig(configPath, cfg),
		withNoError(t),
	)

	as.NoError(eg.Wait())
}

func TestNoPositionalArgSupport(tt *testing.T) {
	t := &test_ui.T{T: tt}
	tempDir := test.TempExamples(t)
	configPath := filepath.Join(tempDir, "/conformist.toml")

	test.ChangeWorkDir(t, tempDir)

	noPositionalArgSupport := true
	cfg := &config.Config{
		FormatterConfigs: map[string]*config.Formatter{
			"echo": {
				Command:                "test-fmt-only-one-file-at-a-time",
				Includes:               []string{"*"},
				NoPositionalArgSupport: &noPositionalArgSupport,
			},
		},
	}

	conformist(
		t,
		withConfig(configPath, cfg),
		withNoError(t),
		withStats(t, map[stats.Type]int{
			stats.Traversed: 33,
			stats.Matched:   33,
			stats.Formatted: 33,
			stats.Changed:   33,
		}),
	)
}

type options struct {
	args []string
	env  map[string]string

	config struct {
		path  string
		value *config.Config
	}

	assertStdout func([]byte)
	assertStderr func([]byte)

	assertError func(*require.Assertions, error)
	assertStats func(*stats.Stats)

	bump struct {
		path    string
		atime   time.Duration
		modtime time.Duration
	}
}

type option func(*options)

func withArgs(args ...string) option {
	return func(o *options) {
		o.args = args
	}
}

func withEnv(env map[string]string) option {
	return func(o *options) {
		o.env = env
	}
}

func withConfig(path string, cfg *config.Config) option {
	return func(o *options) {
		o.config.path = path
		o.config.value = cfg
	}
}

func withConfigFunc(path string, fn func() *config.Config) option {
	return func(o *options) {
		o.config.path = path
		o.config.value = fn()
	}
}

func withStats(t *test_ui.T, expected map[stats.Type]int) option {
	t.Helper()

	return func(o *options) {
		o.assertStats = func(s *stats.Stats) {
			for k, v := range expected {
				require.Equal(t, v, s.Value(k), k.String())
			}
		}
	}
}

func withError(fn func(*require.Assertions, error)) option {
	return func(o *options) {
		o.assertError = fn
	}
}

func withNoError(t *test_ui.T) option {
	t.Helper()

	return func(o *options) {
		o.assertError = func(as *require.Assertions, err error) {
			as.NoError(err)
		}
	}
}

func withStdout(fn func([]byte)) option {
	return func(o *options) {
		o.assertStdout = fn
	}
}

func withStderr(fn func([]byte)) option {
	return func(o *options) {
		o.assertStderr = fn
	}
}

//nolint:unparam
func withModtimeBump(path string, bump time.Duration) option {
	return func(o *options) {
		o.bump.path = path
		o.bump.modtime = bump
	}
}

func conformist(
	t *test_ui.T,
	opt ...option,
) {
	t.Helper()

	as := require.New(t)

	// build options
	opts := &options{}
	for _, option := range opt {
		option(opts)
	}

	// set env
	for k, v := range opts.env {
		t.Logf("setting env %s=%s", k, v)
		t.Setenv(k, v)
	}

	defer func() {
		// unset env variables after executing
		for k := range opts.env {
			t.Setenv(k, "")
		}
	}()

	// default args if nil
	// we must pass an empty array otherwise cobra with use os.Args[1:]
	args := opts.args
	if args == nil {
		args = []string{}
	}

	// write config
	if opts.config.value != nil {
		test.WriteConfig(t, opts.config.path, opts.config.value)
	}

	// bump mod times before running
	if opts.bump.path != "" {
		test.LutimesBump(t, opts.bump.path, opts.bump.atime, opts.bump.modtime)
	}

	t.Logf("conformist %s", strings.Join(args, " "))

	tempDir := t.TempDir()

	tempStdout := test.TempFile(t, tempDir, "stdout", nil)
	tempStderr := test.TempFile(t, tempDir, "stderr", nil)

	// capture standard outputs before swapping them
	stdout := os.Stdout
	stderr := os.Stderr

	// swap them temporarily
	os.Stdout = tempStdout
	os.Stderr = tempStderr

	log.SetOutput(tempStdout)

	defer func() {
		// swap outputs back
		os.Stdout = stdout
		os.Stderr = stderr
		log.SetOutput(stderr)
	}()

	// run the command
	root, statz := cmd.NewRoot("dev", "unknown")

	root.SetArgs(args)
	root.SetOut(tempStdout)
	root.SetErr(tempStderr)

	// execute the command
	cmdErr := root.Execute()

	// reset and read the temporary outputs
	if _, resetErr := tempStdout.Seek(0, 0); resetErr != nil {
		t.Fatal(fmt.Errorf("failed to reset temp output for reading: %w", resetErr))
	}

	if _, resetErr := tempStderr.Seek(0, 0); resetErr != nil {
		t.Fatal(fmt.Errorf("failed to reset temp output for reading: %w", resetErr))
	}

	// read back stderr and validate
	out, readErr := io.ReadAll(tempStderr)
	if readErr != nil {
		t.Fatal(fmt.Errorf("failed to read temp stderr: %w", readErr))
	}

	if opts.assertStderr != nil {
		opts.assertStderr(out)
	}

	t.Log("\n" + string(out))

	// read back stdout and validate
	out, readErr = io.ReadAll(tempStdout)
	if readErr != nil {
		t.Fatal(fmt.Errorf("failed to read temp stdout: %w", readErr))
	}

	t.Log("\n" + string(out))

	if opts.assertStdout != nil {
		opts.assertStdout(out)
	}

	// assert other properties

	if opts.assertStats != nil {
		opts.assertStats(statz)
	}

	if opts.assertError != nil {
		opts.assertError(as, cmdErr)
	}
}
