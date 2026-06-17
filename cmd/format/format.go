package format

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/format"
	"github.com/amarbel-llc/conformist/stats"
	"github.com/amarbel-llc/conformist/walk"
	"github.com/amarbel-llc/conformist/walk/cache"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	bolt "go.etcd.io/bbolt"
)

var ErrFailOnChange = errors.New("unexpected changes detected, --fail-on-change is enabled")

func Run(v *viper.Viper, statz *stats.Stats, cmd *cobra.Command, paths []string) error {
	cmd.SilenceUsage = true

	cfg, err := config.FromViper(v)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.CI {
		log.Info("ci mode enabled")

		startAfter := time.Now().
			// truncate to second precision
			Truncate(time.Second).
			// add one second
			Add(1 * time.Second).
			// a little extra to ensure we don't start until the next second
			Add(10 * time.Millisecond)

		log.Debugf("waiting until %v before continuing", startAfter)

		// Wait until we tick over into the next second before processing to ensure our EPOCH level modtime comparisons
		// for change detection are accurate.
		// This can fail in CI between checkout and running conformist if everything happens too quickly.
		// For humans, the second level precision should not be a problem as they are unlikely to run conformist in
		// sub-second succession.
		time.Sleep(time.Until(startAfter))
	}

	// cpu profiling
	if cfg.CPUProfile != "" {
		cpuProfile, err := os.Create(cfg.CPUProfile)
		if err != nil {
			return fmt.Errorf("failed to open file for writing cpu profile: %w", err)
		} else if err = pprof.StartCPUProfile(cpuProfile); err != nil {
			return fmt.Errorf("failed to start cpu profile: %w", err)
		}

		defer func() {
			pprof.StopCPUProfile()

			if err := cpuProfile.Close(); err != nil {
				log.Errorf("failed to close cpu profile: %v", err)
			}
		}()
	}

	// Remove the cache first before potentially opening a new one.
	if cfg.ClearCache {
		if err := cache.Remove(cfg.TreeRoot); err != nil {
			return fmt.Errorf("failed to clear cache: %w", err)
		}
	}

	var db *bolt.DB

	// open the db unless --no-cache was specified
	if !cfg.NoCache {
		db, err = cache.Open(cfg.TreeRoot)
		if err != nil {
			return fmt.Errorf("failed to open cache: %w", err)
		}

		// ensure db is closed after we're finished
		defer func() {
			if closeErr := db.Close(); closeErr != nil {
				log.Errorf("failed to close cache: %v", closeErr)
			}
		}()
	}

	// create an overall app context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// listen for shutdown signal and cancel the context
	go func() {
		exit := make(chan os.Signal, 1)
		signal.Notify(exit, os.Interrupt, syscall.SIGTERM)
		<-exit
		cancel()
	}()

	return formatTree(ctx, cfg, statz, db, paths)
}

// formatTree runs the linter-repair + formatter pipeline over cfg.TreeRoot,
// scoped to paths, using db for change-detection caching (nil disables it). It
// is the shared core of Run and the staged-blob isolation lane (#40), which
// points cfg.TreeRoot at a temp tree of materialized staged blobs so the working
// tree is never touched.
func formatTree(
	ctx context.Context,
	cfg *config.Config,
	statz *stats.Stats,
	db *bolt.DB,
	paths []string,
) error {
	// parse the walk type
	walkType, err := walk.TypeString(cfg.Walk)
	if err != nil {
		return fmt.Errorf("invalid walk type: %w", err)
	}

	if walkType == walk.Stdin && len(paths) != 1 {
		// check we have only received one path arg which we use for the file extension / matching to formatters
		return errors.New("exactly one path should be specified when using the --stdin flag")
	}

	// Repair-mode linter autofix (RFC 0001 §4): apply configured linter repair
	// commands before formatting, so formatters normalise the autofixed output.
	// This is a separate, cache-less pass so it does not perturb the formatter
	// scheduler/cache below. Skipped in stdin mode.
	if walkType != walk.Stdin {
		if err := applyLinterRepairs(ctx, cfg, statz, walkType, paths); err != nil {
			return fmt.Errorf("failed to apply linter repairs: %w", err)
		}
	}

	// create a composite formatter which will handle applying the correct formatters to each file we traverse
	formatter, err := format.NewCompositeFormatter(cfg, statz, format.BatchSize)
	if err != nil {
		return fmt.Errorf("failed to create composite formatter: %w", err)
	}

	// create a new walker for traversing the paths
	walker, err := walk.NewCompositeReader(walkType, cfg.TreeRoot, paths, db, statz)
	if err != nil {
		return fmt.Errorf("failed to create walker: %w", err)
	}

	// start traversing
	files := make([]*walk.File, format.BatchSize)

	var (
		n                  int
		readErr, formatErr error
	)

	for {
		// read the next batch
		readCtx, cancelRead := context.WithTimeout(ctx, 10*time.Second)

		n, readErr = walker.Read(readCtx, files)
		log.Debugf("read %d files", n)

		// ensure context is cancelled to release resources
		cancelRead()

		// format any files that were read before processing the read error
		if formatErr = formatter.Apply(ctx, files[:n]); formatErr != nil {
			break
		}

		// stop reading files if there was a read error
		if readErr != nil {
			break
		}
	}

	// finalize formatting (there could be formatting tasks in-flight)
	formatCloseErr := formatter.Close(ctx)

	// close the walker, ensuring any pending file release hooks finish
	walkerCloseErr := walker.Close()

	// print stats to stderr
	if !cfg.Quiet {
		statz.PrintToStderr()
	}

	// process errors
	switch {
	case errors.Is(readErr, io.EOF):
		// nothing more to read
		log.Debugf("no more files to read")
	case errors.Is(readErr, context.Canceled):
		// user requested shutdown (e.g. Ctrl+C)
		log.Debugf("context cancelled")
	case errors.Is(readErr, context.DeadlineExceeded):
		// the read timed-out
		return errors.New("timeout reading files")
	case readErr != nil:
		// something unexpected happened
		return fmt.Errorf("failed to read files: %w", readErr)
	}

	if formatErr != nil {
		return fmt.Errorf("failed to format files: %w", formatErr)
	}

	if formatCloseErr != nil {
		return fmt.Errorf("failed to finalise formatting: %w", formatCloseErr)
	}

	if walkerCloseErr != nil {
		return fmt.Errorf("failed to close walker: %w", walkerCloseErr)
	}

	if cfg.FailOnChange && statz.Value(stats.Changed) != 0 {
		// if fail on change has been enabled, check that no files were actually changed, throwing an error if so
		return ErrFailOnChange
	}

	return nil
}

// applyLinterRepairs runs configured linter repair (autofix) commands over the
// tree in a separate, cache-less walk before the formatter pass (RFC 0001 §4).
// It is a no-op when no linter declares a repair command.
func applyLinterRepairs(
	ctx context.Context,
	cfg *config.Config,
	statz *stats.Stats,
	walkType walk.Type,
	paths []string,
) error {
	linter, err := format.NewCompositeLinter(cfg, statz)
	if err != nil {
		return fmt.Errorf("failed to create linter: %w", err)
	}

	if linter.Empty() {
		return nil
	}

	// no cache db: repair always re-runs and never writes cache state
	walker, err := walk.NewCompositeReader(walkType, cfg.TreeRoot, paths, nil, statz)
	if err != nil {
		return fmt.Errorf("failed to create walker for linting: %w", err)
	}

	files := make([]*walk.File, format.BatchSize)

	for {
		readCtx, cancelRead := context.WithTimeout(ctx, 10*time.Second)
		n, readErr := walker.Read(readCtx, files)
		cancelRead()

		if repairErr := linter.Repair(ctx, files[:n]); repairErr != nil {
			_ = walker.Close()

			return fmt.Errorf("linter repair failed: %w", repairErr)
		}

		releaseCtx := walk.SetNoCache(ctx, true)
		for _, file := range files[:n] {
			if releaseErr := file.Release(releaseCtx); releaseErr != nil {
				_ = walker.Close()

				return fmt.Errorf("failed to release file: %w", releaseErr)
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}

			_ = walker.Close()

			return fmt.Errorf("failed to read files for linting: %w", readErr)
		}
	}

	if err := walker.Close(); err != nil {
		return fmt.Errorf("failed to close walker: %w", err)
	}

	return nil
}
