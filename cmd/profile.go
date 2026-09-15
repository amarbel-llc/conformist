package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/profile"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

// The env fallbacks for --profile / --profile-key, honored ONLY under
// --profile-only so a fleet-wide sweatfile env cannot silently change an
// ordinary `conformist check` lane. An explicit flag always wins.
const (
	envProfile     = "CONFORMIST_PROFILE"
	envProfileKeys = "CONFORMIST_PROFILE_KEYS"
)

var (
	errProfileOnlyWithoutProfile = errors.New("--profile-only requires --profile (or " + envProfile + ")")
	errProfileKeyWithoutProfile  = errors.New("--profile-key requires --profile")
)

// profileKeysFromEnv splits CONFORMIST_PROFILE_KEYS on commas and whitespace.
func profileKeysFromEnv() []string {
	return strings.FieldsFunc(os.Getenv(envProfileKeys), func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

// readProfile returns where the profile was read from (a local path made
// absolute) and its bytes, verified when it must be. An https profile is
// fetched and must be signed by a key that is both pinned and published on the
// serving domain (RFC 0005 §3.2). A local file is read as-is, and must be
// signed by a pinned key only when keys are pinned.
func readProfile(
	ctx context.Context, resolver profile.Resolver, path, workingDir string, pinned []string,
) (string, []byte, error) {
	// Any URL goes to FetchSignedProfile, which refuses every scheme but https;
	// only a value with no scheme is a local path.
	if u, err := url.Parse(path); err == nil && u.Scheme != "" {
		data, keyID, err := resolver.FetchSignedProfile(ctx, path, pinned)
		if err != nil {
			return "", nil, fmt.Errorf("fetching signed profile: %w", err)
		}

		log.Infof("profile %s: signature verified with %s", path, keyID)

		return path, data, nil
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("reading profile: %w", err)
	}

	if len(pinned) > 0 {
		keyID, err := profile.VerifySignature(data, pinned)
		if err != nil {
			return "", nil, fmt.Errorf("verifying profile %s: %w", path, err)
		}

		log.Infof("profile %s: signature verified with %s", path, keyID)
	}

	return path, data, nil
}

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

	pinned, err := cmd.Flags().GetStringArray("profile-key")
	if err != nil {
		return nil, fmt.Errorf("reading --profile-key: %w", err)
	}

	if only {
		if path == "" {
			path = os.Getenv(envProfile)
		}

		if len(pinned) == 0 {
			pinned = profileKeysFromEnv()
		}
	}

	if path == "" {
		switch {
		case only:
			return nil, errProfileOnlyWithoutProfile
		case len(pinned) > 0:
			return nil, errProfileKeyWithoutProfile
		}

		return nil, nil //nolint:nilnil // no --profile: nothing to apply, and not an error
	}

	log.Warn("--profile is EXPERIMENTAL (RFC 0005): a single layer, no delegation")

	cacheDir, err := profile.DefaultCacheDir()
	if err != nil {
		return nil, fmt.Errorf("resolving profile: %w", err)
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	resolver := profile.Resolver{CacheDir: cacheDir}

	path, data, err := readProfile(ctx, resolver, path, workingDir, pinned)
	if err != nil {
		return nil, err
	}

	doc, err := profile.Parse(path, data)
	if err != nil {
		return nil, fmt.Errorf("parsing profile: %w", err)
	}

	resolved, err := resolver.Resolve(ctx, doc)
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
