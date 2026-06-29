package format

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"slices"
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

// Run formats/repairs the tree and discards the repair-output paths
// (conformist#55), which only the --staged lane consumes. It is the entry point
// for the bare `conformist` command and `nix fmt`.
func Run(v *viper.Viper, statz *stats.Stats, cmd *cobra.Command, paths []string) error {
	_, err := runWithObserver(v, statz, cmd, paths, runRepair)

	return err
}

// runWithObserver is the shared body of Run and the --staged restage lane. It
// sets up config/cache/signals, runs the format+repair pipeline, and returns the
// opt-in linters' repair-output paths (conformist#55) as reported by observe.
// Run passes the no-op runRepair observer; the staged lane passes a
// git-status-delta observer to learn what to restage.
func runWithObserver(
	v *viper.Viper,
	statz *stats.Stats,
	cmd *cobra.Command,
	paths []string,
	observe repairObserver,
) ([]string, error) {
	cmd.SilenceUsage = true

	cfg, err := config.FromViper(v)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
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
			return nil, fmt.Errorf("failed to open file for writing cpu profile: %w", err)
		} else if err = pprof.StartCPUProfile(cpuProfile); err != nil {
			return nil, fmt.Errorf("failed to start cpu profile: %w", err)
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
			return nil, fmt.Errorf("failed to clear cache: %w", err)
		}
	}

	var db *bolt.DB

	// open the db unless --no-cache was specified
	if !cfg.NoCache {
		db, err = cache.Open(cfg.TreeRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to open cache: %w", err)
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

	return formatTree(ctx, cfg, statz, db, paths, observe)
}

// formatTree runs the linter-repair + formatter pipeline over cfg.TreeRoot,
// scoped to paths, using db for change-detection caching (nil disables it). It
// is the shared core of Run and the staged-blob isolation lane (#40), which
// points cfg.TreeRoot at a temp tree of materialized staged blobs so the working
// tree is never touched.
//
// observe wraps the repair of each opt-in restage-repair-outputs linter
// (conformist#55); formatTree returns the union of paths those linters wrote, as
// reported by the observer. The staged lane passes a git-status-delta observer
// to learn what to restage; every other caller passes runRepair and ignores the
// returned paths.
func formatTree(
	ctx context.Context,
	cfg *config.Config,
	statz *stats.Stats,
	db *bolt.DB,
	paths []string,
	observe repairObserver,
) ([]string, error) {
	// parse the walk type
	walkType, err := walk.TypeString(cfg.Walk)
	if err != nil {
		return nil, fmt.Errorf("invalid walk type: %w", err)
	}

	if walkType == walk.Stdin && len(paths) != 1 {
		// check we have only received one path arg which we use for the file extension / matching to formatters
		return nil, errors.New("exactly one path should be specified when using the --stdin flag")
	}

	// Repair-mode linter autofix (RFC 0001 §4): apply configured linter repair
	// commands before formatting, so formatters normalise the autofixed output.
	// This is a separate, cache-less pass so it does not perturb the formatter
	// scheduler/cache below. Skipped in stdin mode.
	var repairOutputs []string

	if walkType != walk.Stdin {
		repairOutputs, err = applyLinterRepairs(ctx, cfg, statz, walkType, paths, observe)
		if err != nil {
			return nil, fmt.Errorf("failed to apply linter repairs: %w", err)
		}
	}

	// create a composite formatter which will handle applying the correct formatters to each file we traverse
	formatter, err := format.NewCompositeFormatter(cfg, statz, format.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create composite formatter: %w", err)
	}

	// Fold the opt-in linters' repair outputs into the formatter's walk scope
	// (conformist#70) so a whole-tree repair that rewrote a formatter-matched
	// file OUTSIDE the input paths — e.g. doppelgang --fix editing flake.nix
	// while only some other file was staged — has that file normalised before a
	// scoped caller (the --staged lane) restages it. A no-op for a whole-tree
	// walk, which already visits every repair output.
	formatPaths := formatScope(paths, cfg.TreeRoot, repairOutputs)

	// create a new walker for traversing the paths
	walker, err := walk.NewCompositeReader(walkType, cfg.TreeRoot, formatPaths, db, statz)
	if err != nil {
		return nil, fmt.Errorf("failed to create walker: %w", err)
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
		return nil, errors.New("timeout reading files")
	case readErr != nil:
		// something unexpected happened
		return nil, fmt.Errorf("failed to read files: %w", readErr)
	}

	if formatErr != nil {
		return nil, fmt.Errorf("failed to format files: %w", formatErr)
	}

	if formatCloseErr != nil {
		return nil, fmt.Errorf("failed to finalise formatting: %w", formatCloseErr)
	}

	if walkerCloseErr != nil {
		return nil, fmt.Errorf("failed to close walker: %w", walkerCloseErr)
	}

	if cfg.FailOnChange && statz.Value(stats.Changed) != 0 {
		// if fail on change has been enabled, check that no files were actually changed, throwing an error if so
		return nil, ErrFailOnChange
	}

	return repairOutputs, nil
}

// formatScope returns the set of paths the formatter pass should walk: the
// input paths plus any opt-in linter repair outputs (conformist#70) that fall
// outside them, so a whole-tree repair which rewrote a formatter-matched file is
// normalised before a scoped caller restages it.
//
// It only extends an already-scoped walk: an empty paths means a whole-tree walk
// (NewCompositeReader walks the tree root), which already visits every repair
// output — appending to it would wrongly narrow the walk to just those outputs.
// A repair output that no longer exists (a tier-4 deletion, conformist#57) is
// skipped so the walker does not fail to stat an absent path; a duplicate of an
// input path is dropped.
func formatScope(paths []string, treeRoot string, repairOutputs []string) []string {
	if len(paths) == 0 || len(repairOutputs) == 0 {
		return paths
	}

	seen := make(map[string]struct{}, len(paths)+len(repairOutputs))

	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			seen[abs] = struct{}{}
		}
	}

	scope := slices.Clone(paths)

	for _, rel := range repairOutputs {
		abs := filepath.Join(treeRoot, rel)

		if _, dup := seen[abs]; dup {
			continue
		}

		// A repair-driven deletion leaves no file to format; skip it so
		// NewCompositeReader does not error trying to stat an absent path.
		if _, err := os.Stat(abs); err != nil {
			continue
		}

		seen[abs] = struct{}{}
		scope = append(scope, abs)
	}

	return scope
}

// repairObserver wraps the repair of a single opt-in (restage-repair-outputs)
// linter so a caller can observe which paths it wrote (conformist#55). It is
// given the running linter and a closure that performs its repair, and returns
// the toplevel-relative paths the repair touched. The staged lane supplies a
// git-status-delta observer (stagedRepairObserver); a plain repair run supplies
// runRepair, which just runs the repair and reports no paths.
type repairObserver func(ctx context.Context, l *format.Linter, repair func(context.Context) error) ([]string, error)

// runRepair is the no-op observer for non-staged repair runs: it runs the
// opt-in linter's repair and reports no touched paths (nothing consumes them
// outside the staged lane).
func runRepair(ctx context.Context, _ *format.Linter, repair func(context.Context) error) ([]string, error) {
	return nil, repair(ctx)
}

// applyLinterRepairs runs configured linter repair (autofix) commands over the
// tree in a separate, cache-less walk before the formatter pass (RFC 0001 §4).
// It is a no-op when no linter declares a repair command.
//
// Opt-in restage-repair-outputs linters (conformist#55) are run AFTER the walk,
// once over their full accumulated match set, each through observe so the staged
// lane can snapshot around them and learn which paths they wrote. The returned
// slice is the union of those touched paths (sorted, de-duped); it is empty for
// a plain repair run, whose observer (runRepair) reports nothing.
func applyLinterRepairs(
	ctx context.Context,
	cfg *config.Config,
	statz *stats.Stats,
	walkType walk.Type,
	paths []string,
	observe repairObserver,
) ([]string, error) {
	linter, err := format.NewCompositeLinter(cfg, statz)
	if err != nil {
		return nil, fmt.Errorf("failed to create linter: %w", err)
	}

	if linter.Empty() {
		return nil, nil
	}

	// no cache db: repair always re-runs and never writes cache state
	walker, err := walk.NewCompositeReader(walkType, cfg.TreeRoot, paths, nil, statz)
	if err != nil {
		return nil, fmt.Errorf("failed to create walker for linting: %w", err)
	}

	files := make([]*walk.File, format.BatchSize)

	// Accumulate, across batches, the files each opt-in linter matches. Their
	// repair is deferred until the walk completes so it runs once over the full
	// set under a single snapshot (conformist#55). The *walk.File pointers stay
	// valid after Release (Release only runs hooks; it does not recycle File
	// values), and Read writes new pointers into the reused buffer each batch, so
	// appending the pointers out is safe.
	optIn := map[*format.Linter][]*walk.File{}

	for {
		readCtx, cancelRead := context.WithTimeout(ctx, 10*time.Second)
		n, readErr := walker.Read(readCtx, files)
		cancelRead()

		if repairErr := linter.Repair(ctx, files[:n]); repairErr != nil {
			_ = walker.Close()

			return nil, fmt.Errorf("linter repair failed: %w", repairErr)
		}

		for l, fs := range linter.MatchOptInRepair(files[:n]) {
			optIn[l] = append(optIn[l], fs...)
		}

		releaseCtx := walk.SetNoCache(ctx, true)
		for _, file := range files[:n] {
			if releaseErr := file.Release(releaseCtx); releaseErr != nil {
				_ = walker.Close()

				return nil, fmt.Errorf("failed to release file: %w", releaseErr)
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}

			_ = walker.Close()

			return nil, fmt.Errorf("failed to read files for linting: %w", readErr)
		}
	}

	if err := walker.Close(); err != nil {
		return nil, fmt.Errorf("failed to close walker: %w", err)
	}

	// Run each opt-in linter's repair once over its full match set, through the
	// observer, collecting the union of touched paths (conformist#55).
	var touched []string

	for l, fs := range optIn {
		paths, err := observe(ctx, l, func(ctx context.Context) error {
			return linter.RepairLinter(ctx, l, fs)
		})
		if err != nil {
			return nil, fmt.Errorf("linter repair failed: %w", err)
		}

		touched = append(touched, paths...)
	}

	slices.Sort(touched)

	return slices.Compact(touched), nil
}
