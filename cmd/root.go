package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/amarbel-llc/conformist/cmd/format"
	_init "github.com/amarbel-llc/conformist/cmd/init"
	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/stats"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// programName is the binary's self-identification, used in usage and version
// output. Env prefix and config-filename backward-compat for the former
// treelint name are handled in config.NewViper and loadConfig respectively.
const programName = "conformist"

func NewRoot(version, commit string) (*cobra.Command, *stats.Stats) {
	// create a viper instance for reading in config
	v, err := config.NewViper()
	if err != nil {
		cobra.CheckErr(fmt.Errorf("failed to create viper instance: %w", err))
	}

	// create a new stats instance
	statz := stats.New()

	// create our root command
	cmd := &cobra.Command{
		Use:     programName + " [paths...]",
		Short:   "The linter and formatter multiplexer",
		Version: version + "+" + commit,
		// Positional args are paths to format/check. Without an explicit Args
		// validator, cobra treats the first positional as a subcommand name
		// once subcommands (check, version) are registered, breaking
		// `conformist <paths...>` with "unknown command". ArbitraryArgs lets
		// non-subcommand args fall through to RunE while `check`/`version`
		// still dispatch.
		Args: cobra.ArbitraryArgs,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runE(v, &statz, cmd, args)
		},
	}

	// update version template
	cmd.SetVersionTemplate(programName + " {{.Version}}\n")

	// Config flags live on persistent flags so subcommands (e.g. `check`)
	// inherit the same tree-root / walk / excludes / config-file options.
	pfs := cmd.PersistentFlags()
	config.SetFlags(pfs)

	// xor tree-root, tree-root-cmd and tree-root-file flags
	cmd.MarkFlagsMutuallyExclusive(
		"tree-root",
		"tree-root-cmd",
		"tree-root-file",
	)

	pfs.String(
		"config-file", "",
		"Load the config file from the given path (defaults to searching upwards for conformist.toml or "+
			".conformist.toml).",
	)

	// Root-only shortcut flags for the init / completion sub-behaviours.
	fs := cmd.Flags()

	fs.BoolP(
		"init", "i", false,
		"Create a conformist.toml file in the current directory.",
	)

	fs.String(
		"completion", "",
		"[bash|zsh|fish] Generate shell completion scripts for the specified shell.",
	)

	// Auto-commit mode (#24): absorb mechanical fixes as a commit instead of
	// leaving the apply-format-commit round-trip to the caller (hooks chain it
	// with `conformist check` for substantive findings).
	fs.Bool(
		"commit", false,
		"After formatting, commit exactly the files this run changed with the message "+
			"\""+format.CommitMessage+"\". Refuses to run on an unclean working tree (see --allow-dirty). "+
			"Exits 0 if the tree was already conformant, 3 if fixes were committed, 2 if refused.",
	)

	fs.Bool(
		"allow-dirty", false,
		"With --commit, run even if the working tree has uncommitted changes. Files that were "+
			"already dirty before the run are never included in the commit, even if a formatter "+
			"changes them further.",
	)

	fs.StringArray(
		"trailer", nil,
		"With --commit, append a trailer (e.g. \"Co-Fixed-By: tool <tool@example.com>\") to the "+
			"commit message. May be repeated.",
	)

	// Staged scope (#25), lint-staged semantics: the caller's own commit
	// proceeds with conformant content; conformist never commits here.
	fs.Bool(
		"staged", false,
		"Format only the files currently staged in the index and restage the formatted content; "+
			"no commit is created. Refuses partially staged files (staged files that also have "+
			"unstaged changes). Exits 0 if the staged content was already conformant, 3 if files "+
			"were reformatted and restaged, 2 if refused.",
	)

	// --fail-on-change wants changes to fail the run; --commit/--staged want
	// them committed/restaged. --commit and --staged are distinct modes
	// (full-tree fix commit vs lint-staged restage; see #25). (--stdin is
	// rejected by the modes' preflights instead, which exit 2 with a clear
	// message rather than cobra's usage error.)
	cmd.MarkFlagsMutuallyExclusive("fail-on-change", "commit")
	cmd.MarkFlagsMutuallyExclusive("fail-on-change", "staged")
	cmd.MarkFlagsMutuallyExclusive("commit", "staged")

	// bind our config flags to viper
	if err := v.BindPFlags(pfs); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to bind config flags to viper: %w", err))
	}

	// bind prj_root to the tree-root flag, allowing viper to handle environment override for us
	// conforms with https://github.com/numtide/prj-spec/blob/main/PRJ_SPEC.md
	cobra.CheckErr(v.BindPFlag("prj_root", pfs.Lookup("tree-root")))

	cmd.AddCommand(newCheckCmd(v, &statz))
	cmd.AddCommand(newVersionCmd(programName, version, commit))
	cmd.AddCommand(newGenManCmd())
	cmd.AddCommand(newConformCmd())

	return cmd, &statz
}

// changeWorkingDir resolves and changes to the configured working directory,
// returning its absolute path. Shared by the format and check entry points.
func changeWorkingDir(v *viper.Viper) (string, error) {
	workingDir, err := filepath.Abs(v.GetString("working-dir"))
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path for working directory: %w", err)
	}

	if err = os.Chdir(workingDir); err != nil {
		return "", fmt.Errorf("failed to change working directory: %w", err)
	}

	return workingDir, nil
}

func runE(v *viper.Viper, statz *stats.Stats, cmd *cobra.Command, args []string) error {
	flags := cmd.Flags()

	workingDir, err := changeWorkingDir(v)
	if err != nil {
		return err
	}

	// check if we are running the init command
	if init, err := flags.GetBool("init"); err != nil {
		return fmt.Errorf("failed to read init flag: %w", err)
	} else if init {
		if initErr := _init.Run(); initErr != nil {
			return fmt.Errorf("failed to run init command: %w", initErr)
		}

		return nil
	}

	// check if we are running the completion command
	if shell, err := flags.GetString("completion"); err != nil {
		return fmt.Errorf("failed to read completion flag: %w", err)
	} else if shell != "" {
		if completionsErr := generateShellCompletions(cmd, []string{shell}); completionsErr != nil {
			return fmt.Errorf("failed to generate shell completions: %w", completionsErr)
		}

		return nil
	}

	if err := loadConfig(v, cmd, workingDir); err != nil {
		return err
	}

	// auto-commit (#24) and staged (#25) modes
	commit, err := flags.GetBool("commit")
	if err != nil {
		return fmt.Errorf("failed to read commit flag: %w", err)
	}

	staged, err := flags.GetBool("staged")
	if err != nil {
		return fmt.Errorf("failed to read staged flag: %w", err)
	}

	trailers, err := flags.GetStringArray("trailer")
	if err != nil {
		return fmt.Errorf("failed to read trailer flag: %w", err)
	}

	allowDirty, err := flags.GetBool("allow-dirty")
	if err != nil {
		return fmt.Errorf("failed to read allow-dirty flag: %w", err)
	}

	// --staged creates no commit (the caller's commit carries its own
	// message), and tolerates a dirty tree by design — these knobs only make
	// sense with --commit.
	if len(trailers) > 0 && !commit {
		return errors.New("--trailer requires --commit")
	}

	if allowDirty && !commit {
		return errors.New("--allow-dirty requires --commit")
	}

	if staged {
		return format.RunStaged(v, statz, cmd, args) //nolint:wrapcheck
	}

	if commit {
		opts := format.CommitOptions{
			AllowDirty: allowDirty,
			Trailers:   trailers,
		}

		return format.RunCommit(v, statz, cmd, args, opts) //nolint:wrapcheck
	}

	// format
	return format.Run(v, statz, cmd, args) //nolint:wrapcheck
}

// loadConfig discovers and reads the conformist config file into viper and
// configures logging. It assumes the working directory has already been set
// (see changeWorkingDir) and is shared by the format and check entry points.
func loadConfig(v *viper.Viper, cmd *cobra.Command, workingDir string) error {
	flags := cmd.Flags()

	// use the path specified by the flag
	configFile, err := flags.GetString("config-file")
	if err != nil {
		return fmt.Errorf("failed to read config-file flag: %w", err)
	}

	// fallback to env
	if configFile == "" {
		configFile = os.Getenv("CONFORMIST_CONFIG")
	}

	// conformist.toml is preferred; treelint.toml is the legacy fallback from
	// before the treelint -> conformist rename. Earlier entries win.
	filenames := []string{"conformist.toml", ".conformist.toml", "treelint.toml", ".treelint.toml"}

	// look in PRJ_ROOT if set
	if prjRoot := os.Getenv("PRJ_ROOT"); configFile == "" && prjRoot != "" {
		configFile, _ = config.Find(prjRoot, filenames...)
	}

	// search up from the working directory
	if configFile == "" {
		configFile, _, err = config.FindUp(workingDir, filenames...)
	}

	// error out if we couldn't find the config file
	if err != nil {
		cmd.SilenceUsage = true

		return fmt.Errorf("failed to find conformist config file: %w", err)
	}

	log.Debugf("using config file: %s", configFile)

	// read in the config
	v.SetConfigFile(configFile)

	if err := v.ReadInConfig(); err != nil {
		cobra.CheckErr(fmt.Errorf("failed to read config file '%s': %w", configFile, err))
	}

	// configure logging
	log.SetOutput(os.Stderr)
	log.SetReportTimestamp(false)

	if v.GetBool("quiet") {
		// if quiet, we only log errors
		log.SetLevel(log.ErrorLevel)
	} else {
		// otherwise, the verbose flag controls the log level
		switch v.GetInt("verbose") {
		case 0:
			log.SetLevel(log.WarnLevel)
		case 1:
			log.SetLevel(log.InfoLevel)
		default:
			log.SetLevel(log.DebugLevel)
		}
	}

	return nil
}
