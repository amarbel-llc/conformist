package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	formatCmd "code.linenisgreat.com/conformist/cmd/format"
	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/format"
	"code.linenisgreat.com/conformist/stats"
	"code.linenisgreat.com/conformist/walk"
	"code.linenisgreat.com/conformist/walk/cache"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	bolt "go.etcd.io/bbolt"
)

// ErrCheckFindings indicates `conformist check` found at least one finding
// (RFC 0001 §7, exit code 1). ErrCheckOperational indicates an operational
// failure such as a missing executable or invalid config (exit code 2).
var (
	ErrCheckFindings    = errors.New("one or more findings were detected")
	ErrCheckOperational = errors.New("check failed")
)

// ExitCode maps a command error to a process exit code. The `check` subcommand
// distinguishes findings (1) from operational failures (2) per RFC 0001 §7.
// Repair mode's --commit (#24) and --staged (#25) flags add 3 (fixes were
// applied and committed/restaged) and map their refusals (dirty tree, partial
// staging, not a git worktree, leftover conflict markers #67, a refused
// config-identity mismatch #76) to 2; `conform` (#17) also exits 3 when it
// scaffolds files; all other errors exit 1.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrCheckFindings):
		return 1
	case errors.Is(err, ErrCheckOperational),
		errors.Is(err, formatCmd.ErrCommitRefused),
		errors.Is(err, formatCmd.ErrConflictMarkers),
		errors.Is(err, formatCmd.ErrStagedRefused),
		errors.Is(err, formatCmd.ErrIdentityMismatch),
		errors.Is(err, ErrConformFailed):
		return 2
	case errors.Is(err, formatCmd.ErrFixesCommitted),
		errors.Is(err, formatCmd.ErrFixesRestaged),
		errors.Is(err, ErrScaffolded):
		return 3
	default:
		return 1
	}
}

func newCheckCmd(v *viper.Viper, statz *stats.Stats) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check [paths...]",
		Short: "Check formatting and run linters without modifying any files",
		Long: "Evaluate every configured formatter and linter in read-only check mode. " +
			"Formatters with a native check command are run directly; fix-only formatters are " +
			"checked via a sandbox copy so the working tree is never written. Exits 0 when clean, " +
			"1 when findings are detected, and 2 on an operational error.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(v, statz, cmd, args)
		},
	}

	// Local, not persistent, so it never reaches viper's config decoding.
	cmd.Flags().String(
		"profile", "",
		"EXPERIMENTAL (RFC 0005): resolve this conformist profile — a local path, or an https URL "+
			"verified with --profile-key — before checking: fetch and verify its pinned artifacts, put "+
			"the executable ones on PATH, and add its linter stanzas (conformist.toml wins on a name "+
			"clash). Single-layer, no delegation; not for production use.",
	)
	cmd.Flags().StringArray(
		"profile-key", nil,
		"EXPERIMENTAL (RFC 0005): pin a slot-9A signing key the profile must be signed with, as a "+
			"piggy-piv_auth-v1@ssh_ecdsa_nistp256_pub markl-id; repeatable. Required when --profile is an "+
			"https URL (e.g. https://api.linenisgreat.com/papi/conformist-profile), where the key must also "+
			"be published on that domain's /papi/piggy-ids. With a local --profile, the file must then be "+
			"signed by a pinned key.",
	)
	cmd.Flags().Bool(
		"profile-only", false,
		"With --profile, run ONLY the profile's rules: skip every formatter and linter from the config "+
			"(which then need not exist), so the exit code is the profile's verdict alone.",
	)

	return cmd
}

func runCheck(v *viper.Viper, statz *stats.Stats, cmd *cobra.Command, paths []string) error {
	cmd.SilenceUsage = true

	workingDir, err := changeWorkingDir(v)
	if err != nil {
		return err
	}

	if err := loadConfig(v, cmd, workingDir); err != nil {
		return err
	}

	cfg, err := config.FromViper(v)
	if err != nil {
		return fmt.Errorf("%w: failed to load config: %w", ErrCheckOperational, err)
	}

	profileSrc, err := applyProfile(cmd, cfg, workingDir)
	if err != nil {
		return fmt.Errorf("%w: profile: %w", ErrCheckOperational, err)
	}

	walkType, err := walk.TypeString(cfg.Walk)
	if err != nil {
		return fmt.Errorf("%w: invalid walk type: %w", ErrCheckOperational, err)
	}

	checker, err := format.NewCompositeChecker(cfg, statz)
	if err != nil {
		return fmt.Errorf("%w: failed to create checker: %w", ErrCheckOperational, err)
	}

	// Whole-tree check caching (conformist#16): clear the cache first if asked,
	// then open it unless --no-cache. This db holds only check signatures, never
	// tree contents — check mode still never writes the working tree. The walker
	// stays cache-less (per-file checks are not cached); only the whole-tree checks
	// consult this db, via the checker.
	// Caching is best-effort: a read-only sandbox (e.g. the nix checks.formatting
	// build, where HOME=/homeless-shelter) has no writable cache dir, so a cache
	// failure degrades to an uncached run rather than failing the check.
	if cfg.ClearCache {
		if rmErr := cache.Remove(cfg.TreeRoot); rmErr != nil {
			log.Debugf("cache clear skipped (cache unavailable): %v", rmErr)
		}
	}

	var db *bolt.DB

	if !cfg.NoCache {
		opened, openErr := cache.Open(cfg.TreeRoot)
		if openErr != nil {
			log.Debugf("whole-tree check caching disabled (cache unavailable): %v", openErr)
		} else {
			db = opened

			defer func() {
				if closeErr := db.Close(); closeErr != nil {
					log.Errorf("failed to close cache: %v", closeErr)
				}
			}()
		}
	}

	checker.SetCache(db, cfg.NoCache)

	// The walker is cache-less in check mode: only whole-tree checks are cached
	// (by the checker, above); per-file checks always run.
	walker, err := walk.NewCompositeReader(walkType, cfg.TreeRoot, paths, nil, statz)
	if err != nil {
		return fmt.Errorf("%w: failed to create walker: %w", ErrCheckOperational, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		exit := make(chan os.Signal, 1)
		signal.Notify(exit, os.Interrupt, syscall.SIGTERM)
		<-exit
		cancel()
	}()

	files := make([]*walk.File, format.BatchSize)

	var (
		findings []format.Finding
		readErr  error
	)

	for {
		readCtx, cancelRead := context.WithTimeout(ctx, 10*time.Second)
		n, rErr := walker.Read(readCtx, files)
		cancelRead()

		readErr = rErr

		batch := files[:n]

		batchFindings, checkErr := checker.Check(ctx, batch)
		if checkErr != nil {
			_ = walker.Close()

			return fmt.Errorf("%w: %w", ErrCheckOperational, checkErr)
		}

		findings = append(findings, batchFindings...)

		// release the batch; check mode never updates the cache
		releaseCtx := walk.SetNoCache(ctx, true)
		for _, file := range batch {
			if releaseErr := file.Release(releaseCtx); releaseErr != nil {
				_ = walker.Close()

				return fmt.Errorf("%w: failed to release file: %w", ErrCheckOperational, releaseErr)
			}
		}

		if readErr != nil {
			break
		}
	}

	if closeErr := walker.Close(); closeErr != nil {
		return fmt.Errorf("%w: failed to close walker: %w", ErrCheckOperational, closeErr)
	}

	switch {
	case readErr == nil, errors.Is(readErr, io.EOF):
		// nothing more to read
	case errors.Is(readErr, context.Canceled):
		log.Debugf("context cancelled")
	case errors.Is(readErr, context.DeadlineExceeded):
		return fmt.Errorf("%w: timeout reading files", ErrCheckOperational)
	default:
		return fmt.Errorf("%w: failed to read files: %w", ErrCheckOperational, readErr)
	}

	// Run whole-tree (passes-files=false) checks now that every batch has been
	// read and their full matched sets accumulated (conformist#16).
	wholeTreeFindings, finErr := checker.Finalize(ctx)
	if finErr != nil {
		return fmt.Errorf("%w: %w", ErrCheckOperational, finErr)
	}

	findings = append(findings, wholeTreeFindings...)

	if !cfg.Quiet {
		statz.PrintToStderr()
	}

	if len(findings) > 0 {
		reportFindings(findings)
	}

	if profileSrc != nil {
		reportFindingsBySource(findings, profileSrc)
	}

	if len(findings) > 0 {
		return ErrCheckFindings
	}

	return nil
}

// reportFindingsBySource prints one verdict line per configuration source. The
// exit code is shared between a profile and the config it runs beside, so
// without this a lane cannot tell whether the profile's rules or a
// pre-existing config finding failed it.
func reportFindingsBySource(findings []format.Finding, src *profileSource) {
	var fromProfile, fromConfig []string

	for _, f := range findings {
		if f.Kind == format.FindingLint && src.linters[f.Tool] {
			fromProfile = append(fromProfile, f.Tool)
		} else {
			fromConfig = append(fromConfig, f.Tool)
		}
	}

	fmt.Fprintf(os.Stdout, "profile %s: %s\n", src.path, sourceVerdict(fromProfile))

	if !src.only {
		fmt.Fprintf(os.Stdout, "config: %s\n", sourceVerdict(fromConfig))
	}
}

func sourceVerdict(tools []string) string {
	if len(tools) == 0 {
		return "clean"
	}

	slices.Sort(tools)

	return "findings from " + strings.Join(slices.Compact(tools), ", ")
}

func reportFindings(findings []format.Finding) {
	for _, f := range findings {
		switch f.Kind {
		case format.FindingFormat:
			if f.Path != "" {
				fmt.Fprintf(os.Stdout, "would reformat: %s (%s)\n", f.Path, f.Tool)
			} else {
				fmt.Fprintf(os.Stdout, "formatting needed (%s)\n", f.Tool)
			}
		case format.FindingLint:
			fmt.Fprintf(os.Stdout, "lint findings (%s)\n", f.Tool)
		}
	}
}
