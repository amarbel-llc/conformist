package format

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/amarbel-llc/conformist/config"
	"github.com/amarbel-llc/conformist/stats"
	"github.com/amarbel-llc/conformist/walk"
	"github.com/amarbel-llc/conformist/walk/cache"
	"github.com/charmbracelet/log"
	"github.com/gobwas/glob"
	bolt "go.etcd.io/bbolt"
	"mvdan.cc/sh/v3/expand"
)

// FindingKind distinguishes a formatting finding (a formatter would change the
// file) from a lint finding (a linter reported a problem).
type FindingKind string

const (
	FindingFormat FindingKind = "format"
	FindingLint   FindingKind = "lint"
)

// Finding records a single check result: a tool reported that something is not
// conformant. Path is set for formatter findings; linter findings are reported
// at the tool (invocation) level since linters do not attribute per file.
type Finding struct {
	Tool string
	Kind FindingKind
	Path string
}

// CompositeChecker runs formatters and linters in read-only check mode (RFC 0001
// §5). It never writes to the tree root.
type CompositeChecker struct {
	cfg            *config.Config
	stats          *stats.Stats
	globalExcludes []glob.Glob
	unmatchedLevel log.Level

	formatters map[string]*Formatter
	linters    map[string]*Linter

	// Whole-tree (passes-files=false) check caching (conformist#16). db is nil
	// and/or noCache is true when caching is disabled. wholeTreeFiles accumulates
	// each whole-tree check's matched files across Check calls so Finalize can run
	// it once over its full set.
	db             *bolt.DB
	noCache        bool
	wholeTreeFiles map[*Linter][]*walk.File
}

// SetCache enables whole-tree check caching against db (conformist#16). A nil db
// or noCache=true disables it: every whole-tree check runs. Per-file tools are
// unaffected — the checker never caches them.
func (c *CompositeChecker) SetCache(db *bolt.DB, noCache bool) {
	c.db = db
	c.noCache = noCache
}

// Check evaluates the given files, returning the findings discovered. A non-nil
// error indicates an operational failure (RFC 0001 §7 error class), not findings.
func (c *CompositeChecker) Check(ctx context.Context, files []*walk.File) ([]Finding, error) {
	formatterFiles := map[*Formatter][]*walk.File{}
	linterFiles := map[*Linter][]*walk.File{}

	for _, file := range files {
		// A globally-excluded file is kept from formatters (the "don't rewrite"
		// intent) and from per-file linters (which receive the file as input, so
		// "don't rewrite" is equally "don't inspect the contents of"). A
		// whole-tree check (passes-files=false) is exempt: its includes are a
		// trigger gate, not an input set — the matched file is never passed to
		// it — so the "don't rewrite" intent is irrelevant and it may watch a
		// globally-excluded file like go.mod (conformist#45, retiring the
		// conformist#44 ignore-global-excludes flag).
		globallyExcluded := pathMatches(file.RelPath, c.globalExcludes)

		matched := false

		if !globallyExcluded {
			for _, f := range c.formatters {
				if f.Wants(file) {
					formatterFiles[f] = append(formatterFiles[f], file)
					matched = true
				}
			}
		}

		for _, l := range c.linters {
			if globallyExcluded && l.passesFiles {
				continue
			}

			if l.Wants(file) {
				linterFiles[l] = append(linterFiles[l], file)
				matched = true
			}
		}

		// A globally-excluded file that no opt-in linter wanted is silently
		// skipped, exactly as before — it must not trip the unmatched path.
		if globallyExcluded && !matched {
			continue
		}

		switch {
		case matched:
			c.stats.Add(stats.Matched, 1)
		case c.unmatchedLevel == log.FatalLevel:
			return nil, fmt.Errorf("no formatter or linter for path: %s", file.RelPath)
		default:
			log.Logf(c.unmatchedLevel, "no formatter or linter for path: %s", file.RelPath)
		}
	}

	var findings []Finding

	for f, fs := range formatterFiles {
		// Progress output: a fix-only formatter checked via sandbox-and-diff
		// can run for a long time over a large file set with no other signal,
		// making a slow run indistinguishable from a hang. Announce each
		// formatter (and its file count) before it runs so -v surfaces which
		// tool is active. Per-phase (copy vs. run) detail is logged at -vv in
		// checkSandbox.
		log.Infof("checking %d files with formatter %q", len(fs), f.Name())

		found, err := f.check(ctx, c.cfg.TreeRoot, fs)
		if err != nil {
			return nil, err
		}

		log.Debugf("formatter %q: %d findings", f.Name(), len(found))

		findings = append(findings, found...)
	}

	for l, fs := range linterFiles {
		if !l.passesFiles {
			// Whole-tree check: accumulate its matched files and defer execution to
			// Finalize (after all batches), so its cache key covers the full matched
			// set and it runs once per tree rather than once per batch.
			c.wholeTreeFiles[l] = append(c.wholeTreeFiles[l], fs...)

			continue
		}

		log.Infof("checking %d files with linter %q", len(fs), l.Name())

		hasFindings, output, err := l.Check(ctx, fs)
		if err != nil {
			return nil, err
		}

		if hasFindings {
			if output != "" {
				fmt.Fprintln(os.Stderr, output)
			}

			findings = append(findings, Finding{Tool: l.Name(), Kind: FindingLint})
		}
	}

	return findings, nil
}

// Finalize runs the whole-tree (passes-files=false) checks accumulated across
// Check calls, each exactly once over its full matched set. A check is skipped
// when its cache entry matches the current config + matched-file-set signature;
// otherwise it runs, and a clean run (no findings) is cached. A check with
// findings is never cached, so it re-reports until fixed. Call once, after the
// final Check. Returns the lint findings discovered.
func (c *CompositeChecker) Finalize(ctx context.Context) ([]Finding, error) {
	// stable order for deterministic output and logging
	linters := make([]*Linter, 0, len(c.wholeTreeFiles))
	for l := range c.wholeTreeFiles {
		linters = append(linters, l)
	}

	sort.Slice(linters, func(i, j int) bool { return linters[i].name < linters[j].name })

	var findings []Finding

	for _, l := range linters {
		matched := c.wholeTreeFiles[l]
		if len(matched) == 0 {
			continue
		}

		key := wholeTreeSignature(l, matched)

		if c.wholeTreeCached(l.name, key) {
			log.Debugf("whole-tree check %q: nothing changed, skipping (cached)", l.name)

			continue
		}

		log.Infof("running whole-tree check %q", l.name)

		hasFindings, output, err := l.Check(ctx, matched)
		if err != nil {
			return nil, err
		}

		if hasFindings {
			if output != "" {
				fmt.Fprintln(os.Stderr, output)
			}

			findings = append(findings, Finding{Tool: l.name, Kind: FindingLint})

			// not cached: a failing check must re-report until fixed
			continue
		}

		c.wholeTreeStore(l.name, key)
	}

	return findings, nil
}

// wholeTreeSignature is a whole-tree check's cache key: a hash of its config
// (command, options, includes, excludes) combined with an order-independent
// digest of its matched files' (rel-path, mod-time, size). A config change or
// any added, removed, or modified matched file changes the key.
func wholeTreeSignature(l *Linter, files []*walk.File) []byte {
	cfgHash := sha256.New()
	cfgHash.Write([]byte(l.config.Command))

	for _, parts := range [][]string{l.config.Options, l.config.Includes, l.config.Excludes} {
		cfgHash.Write([]byte{0})

		for _, p := range parts {
			cfgHash.Write([]byte(p))
			cfgHash.Write([]byte{0})
		}
	}

	digests := make([][]byte, 0, len(files))

	for _, f := range files {
		fh := sha256.New()
		fh.Write([]byte(f.RelPath))

		if f.Info != nil {
			// second precision + size, mirroring the per-file format cache
			fh.Write(fmt.Appendf(nil, "\x00%d\x00%d", f.Info.ModTime().Unix(), f.Info.Size()))
		}

		digests = append(digests, fh.Sum(nil))
	}

	sort.Slice(digests, func(i, j int) bool { return bytes.Compare(digests[i], digests[j]) < 0 })

	key := sha256.New()
	key.Write(cfgHash.Sum(nil))

	for _, d := range digests {
		key.Write(d)
	}

	return key.Sum(nil)
}

func (c *CompositeChecker) wholeTreeCached(name string, key []byte) bool {
	if c.db == nil || c.noCache {
		return false
	}

	var cached bool

	if err := c.db.View(func(tx *bolt.Tx) error {
		bucket := cache.WholeTreeBucket(tx)
		if bucket == nil {
			return nil
		}

		cached = bytes.Equal(bucket.Get([]byte(name)), key)

		return nil
	}); err != nil {
		log.Debugf("whole-tree cache read failed for %q: %v", name, err)

		return false
	}

	return cached
}

func (c *CompositeChecker) wholeTreeStore(name string, key []byte) {
	if c.db == nil || c.noCache {
		return
	}

	if err := c.db.Update(func(tx *bolt.Tx) error {
		bucket := cache.WholeTreeBucket(tx)
		if bucket == nil {
			return nil
		}

		return bucket.Put([]byte(name), key)
	}); err != nil {
		log.Debugf("whole-tree cache write failed for %q: %v", name, err)
	}
}

// NewCompositeChecker builds a checker from config, initialising the configured
// formatters and linters.
func NewCompositeChecker(cfg *config.Config, statz *stats.Stats) (*CompositeChecker, error) {
	globalExcludes, err := compileGlobs(cfg.Excludes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile global excludes: %w", err)
	}

	unmatchedLevel, err := log.ParseLevel(cfg.OnUnmatched)
	if err != nil {
		return nil, fmt.Errorf("invalid on-unmatched value: %w", err)
	}

	env := expand.ListEnviron(os.Environ()...)

	formatters := map[string]*Formatter{}

	for name, fCfg := range cfg.FormatterConfigs {
		formatter, err := newFormatter(name, cfg.TreeRoot, env, fCfg)
		if errors.Is(err, ErrCommandNotFound) && cfg.AllowMissingFormatter {
			log.Debugf("formatter command not found: %v", name)

			continue
		} else if err != nil {
			return nil, fmt.Errorf("failed to initialise formatter %v: %w", name, err)
		}

		formatters[name] = formatter
	}

	linters := map[string]*Linter{}

	for name, lCfg := range cfg.LinterConfigs {
		linter, err := newLinter(name, cfg.TreeRoot, env, lCfg)
		if errors.Is(err, ErrCommandNotFound) && cfg.AllowMissingFormatter {
			log.Debugf("linter command not found: %v", name)

			continue
		} else if err != nil {
			return nil, fmt.Errorf("failed to initialise linter %v: %w", name, err)
		}

		linters[name] = linter
	}

	return &CompositeChecker{
		cfg:            cfg,
		stats:          statz,
		globalExcludes: globalExcludes,
		unmatchedLevel: unmatchedLevel,
		formatters:     formatters,
		linters:        linters,
		wholeTreeFiles: map[*Linter][]*walk.File{},
	}, nil
}
