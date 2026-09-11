package cmd

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/profile"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

// applyProfile implements `check --profile` (RFC 0005 POC v1): parse the
// profile, verify and materialize its pinned artifacts, prepend the executable
// ones to PATH, and merge its linter stanzas into cfg. conformist.toml wins on a
// name clash (RFC 0005 §4.3). It must run before the checker is built, because
// tool lookup reads PATH at construction.
func applyProfile(cmd *cobra.Command, cfg *config.Config, workingDir string) error {
	path, err := cmd.Flags().GetString("profile")
	if err != nil {
		return fmt.Errorf("reading --profile: %w", err)
	}

	if path == "" {
		return nil
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}

	log.Warn("--profile is EXPERIMENTAL (RFC 0005 POC v1): a single local layer, no signature verification")

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading profile: %w", err)
	}

	doc, err := profile.Parse(path, data)
	if err != nil {
		return fmt.Errorf("parsing profile: %w", err)
	}

	cacheDir, err := profile.DefaultCacheDir()
	if err != nil {
		return fmt.Errorf("resolving profile: %w", err)
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	resolved, err := profile.Resolver{CacheDir: cacheDir}.Resolve(ctx, doc)
	if err != nil {
		return fmt.Errorf("resolving profile: %w", err)
	}

	if len(resolved.PathDirs) > 0 {
		entries := slices.Concat(resolved.PathDirs, []string{os.Getenv("PATH")})
		if err := os.Setenv("PATH", strings.Join(entries, string(os.PathListSeparator))); err != nil {
			return fmt.Errorf("extending PATH: %w", err)
		}
	}

	if cfg.LinterConfigs == nil {
		cfg.LinterConfigs = map[string]*config.Linter{}
	}

	for _, name := range slices.Sorted(maps.Keys(cfg.LinterConfigs)) {
		log.Debugf("linter %s: supplied by conformist.toml", name)
	}

	for _, name := range slices.Sorted(maps.Keys(resolved.Linters)) {
		if _, clash := cfg.LinterConfigs[name]; clash {
			log.Infof("linter %s: conformist.toml overrides the stanza in profile %s", name, path)

			continue
		}

		cfg.LinterConfigs[name] = resolved.Linters[name]
		log.Debugf("linter %s: supplied by profile %s", name, path)
	}

	return nil
}
