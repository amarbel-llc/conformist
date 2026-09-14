package cmd

import (
	"context"
	"errors"
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

var errProfileOnlyWithoutProfile = errors.New("--profile-only requires --profile")

// profileSource records what a `check --profile` run took from the profile, so
// findings can be attributed to it rather than to the config.
type profileSource struct {
	path string
	// linters are the profile stanzas actually added (a name conformist.toml
	// overrode is the config's, not the profile's).
	linters map[string]bool
	// only is true under --profile-only: the config contributed nothing.
	only bool
}

// profileOnlyRequested reports whether --profile-only is set. Safe on commands
// that do not define the flag.
func profileOnlyRequested(cmd *cobra.Command) bool {
	flag := cmd.Flags().Lookup("profile-only")

	return flag != nil && flag.Value.String() == "true"
}

// applyProfile implements `check --profile` (RFC 0005 POC v1): parse the
// profile, verify and materialize its pinned artifacts, prepend the executable
// ones to PATH, and merge its linter stanzas into cfg. conformist.toml wins on a
// name clash (RFC 0005 §4.3), unless --profile-only drops the config's tools
// entirely. It must run before the checker is built, because tool lookup reads
// PATH at construction. It returns nil when no profile was requested.
func applyProfile(cmd *cobra.Command, cfg *config.Config, workingDir string) (*profileSource, error) {
	path, err := cmd.Flags().GetString("profile")
	if err != nil {
		return nil, fmt.Errorf("reading --profile: %w", err)
	}

	only := profileOnlyRequested(cmd)

	if path == "" {
		if only {
			return nil, errProfileOnlyWithoutProfile
		}

		return nil, nil //nolint:nilnil // no --profile: nothing to apply, and not an error
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}

	log.Warn("--profile is EXPERIMENTAL (RFC 0005 POC v1): a single local layer, no signature verification")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading profile: %w", err)
	}

	doc, err := profile.Parse(path, data)
	if err != nil {
		return nil, fmt.Errorf("parsing profile: %w", err)
	}

	cacheDir, err := profile.DefaultCacheDir()
	if err != nil {
		return nil, fmt.Errorf("resolving profile: %w", err)
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	resolved, err := profile.Resolver{CacheDir: cacheDir}.Resolve(ctx, doc)
	if err != nil {
		return nil, fmt.Errorf("resolving profile: %w", err)
	}

	if len(resolved.PathDirs) > 0 {
		entries := slices.Concat(resolved.PathDirs, []string{os.Getenv("PATH")})
		if err := os.Setenv("PATH", strings.Join(entries, string(os.PathListSeparator))); err != nil {
			return nil, fmt.Errorf("extending PATH: %w", err)
		}
	}

	if only {
		if n := len(cfg.FormatterConfigs) + len(cfg.LinterConfigs); n > 0 {
			log.Infof("--profile-only: skipping %d formatter/linter stanza(s) from the config", n)
		}

		cfg.FormatterConfigs = nil
		cfg.LinterConfigs = nil
	}

	if cfg.LinterConfigs == nil {
		cfg.LinterConfigs = map[string]*config.Linter{}
	}

	for _, name := range slices.Sorted(maps.Keys(cfg.LinterConfigs)) {
		log.Debugf("linter %s: supplied by conformist.toml", name)
	}

	src := &profileSource{path: path, linters: map[string]bool{}, only: only}

	for _, name := range slices.Sorted(maps.Keys(resolved.Linters)) {
		if _, clash := cfg.LinterConfigs[name]; clash {
			log.Infof("linter %s: conformist.toml overrides the stanza in profile %s", name, path)

			continue
		}

		cfg.LinterConfigs[name] = resolved.Linters[name]
		src.linters[name] = true
		log.Debugf("linter %s: supplied by profile %s", name, path)
	}

	return src, nil
}
