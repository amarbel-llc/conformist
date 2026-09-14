package profile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/rulejq"
)

var (
	ErrFetch             = errors.New("fetching artifact")
	ErrUnsupportedScheme = errors.New("unsupported artifact url scheme (POC v1 fetches file:// and https://)")
	ErrArtifactTooLarge  = errors.New("artifact exceeds the size limit")
	ErrCachedCorrupt     = errors.New("cached artifact failed verification")
	// ErrArtifactNeedsLogin marks a url that sent an anonymous fetch to log in.
	ErrArtifactNeedsLogin = errors.New(
		"artifact url is not publicly downloadable (redirected to log in); pin a url that serves it anonymously",
	)
)

// maxArtifactBytes bounds a single fetched artifact. A static `just` is a few
// MiB; the limit only exists so a wrong url cannot exhaust memory.
const maxArtifactBytes = 256 << 20

// ruleToolArgs maps each supported rule-tool to the shell words that run it
// over standard input with the rule file as "$1". The rule reaches the tool as
// a file, never as shell text (RFC 0005 §4.4). `jq` is conformist's embedded
// gojq (rulejq), served in-process, so a rule never depends on a jq on PATH.
var ruleToolArgs = map[string]string{
	"jq": rulejq.Command + ` -f "$1"`,
}

// ruleCommandTemplate wraps a stanza's command and its rule tool into one whole-
// tree check. Any rule output is a finding (exit 1). pipefail matters: without
// it a failing producer (say, a `just` that cannot parse the justfile) would
// feed the rule empty input and pass vacuously.
const ruleCommandTemplate = `set -o pipefail
out=$({
%s
} | %s) || {
  status=$?
  printf '%%s\n' "$out"
  echo "conformist: profile rule pipeline failed (exit $status)" >&2
  exit 2
}
if [ -n "$out" ]; then
  printf '%%s\n' "$out"
  exit 1
fi`

// Resolved is the outcome of resolving a profile: what to put on PATH and the
// linter configs to merge into conformist.toml's.
type Resolved struct {
	// PathDirs are the directories holding executable artifacts, in artifact-
	// name order, to prepend to PATH for the run (RFC 0005 §4.2).
	PathDirs []string
	// Linters are the translated stanzas, keyed by linter name.
	Linters map[string]*config.Linter
}

// Resolver materializes a profile's artifacts into a content-addressed cache.
type Resolver struct {
	// CacheDir is the root of the materialized-artifact cache.
	CacheDir string
	// Client fetches https:// artifacts; nil uses a client with a timeout.
	Client *http.Client
}

// DefaultCacheDir is `$XDG_CACHE_HOME/conformist/profile` (or the platform
// equivalent).
func DefaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating the user cache dir: %w", err)
	}

	return filepath.Join(base, "conformist", "profile"), nil
}

// Resolve verifies every pin, then fetches, verifies and materializes each
// artifact and translates the linter stanzas. Any failure aborts the whole
// resolution: nothing unverified is ever written to the cache or run.
func (r Resolver) Resolve(ctx context.Context, doc *Document) (*Resolved, error) {
	// Check every pin before fetching anything (RFC 0005 §2).
	pins := make(map[string]MarklID, len(doc.Artifacts))

	for _, name := range sortedKeys(doc.Artifacts) {
		id, err := ParseMarklID(doc.Artifacts[name].Markl)
		if err != nil {
			return nil, fmt.Errorf("artifact %q: %w", name, err)
		}

		pins[name] = id
	}

	res := &Resolved{Linters: map[string]*config.Linter{}}
	paths := make(map[string]string, len(doc.Artifacts))
	pinList := make([]string, 0, len(doc.Artifacts))

	for _, name := range sortedKeys(doc.Artifacts) {
		a := doc.Artifacts[name]

		path, err := r.materialize(ctx, name, a, pins[name])
		if err != nil {
			return nil, err
		}

		paths[name] = path
		pinList = append(pinList, name+"="+pins[name].String())

		if a.IsExecutable() {
			res.PathDirs = append(res.PathDirs, filepath.Dir(path))
		}
	}

	// A linter's cache key hashes its command text, not the binaries that text
	// invokes. Recording the pins in the command (as a shell comment) makes a
	// changed artifact invalidate every profile linter's cached result, which
	// the bare `just` in the command could never do on its own (RFC 0005 §4.2).
	pinComment := "\n# conformist profile pins: " + strings.Join(pinList, " ")

	for _, name := range sortedKeys(doc.Linters) {
		lc, err := r.translate(doc, doc.Linters[name], paths)
		if err != nil {
			return nil, fmt.Errorf("linter %q: %w", name, err)
		}

		lc.Command += pinComment
		res.Linters[name] = lc
	}

	return res, nil
}

func (r Resolver) translate(doc *Document, l Linter, artifactPaths map[string]string) (*config.Linter, error) {
	lc := &config.Linter{
		Command:       l.Command,
		Options:       l.Options,
		Includes:      l.Includes,
		Excludes:      l.Excludes,
		Priority:      l.Priority,
		PassesFiles:   l.PassesFiles,
		RepairCommand: l.RepairCommand,
		RepairOptions: l.RepairOptions,
		WorkingDir:    l.WorkingDir,
	}

	if !l.HasRule() {
		return lc, nil
	}

	program, err := assembleProgram(doc, l, artifactPaths)
	if err != nil {
		return nil, err
	}

	rulePath, err := r.writeRule(program)
	if err != nil {
		return nil, err
	}

	lc.Command = fmt.Sprintf(ruleCommandTemplate, l.Command, ruleToolArgs[l.RuleTool])
	lc.Options = []string{rulePath}

	return lc, nil
}

// assembleProgram joins a rule's preludes, in the order it lists them, then the
// rule itself, into the one program its rule-tool runs. Each part is inline
// text or the verified bytes of a materialized data artifact. Order matters: jq
// requires a definition to precede its use.
func assembleProgram(doc *Document, l Linter, artifactPaths map[string]string) (string, error) {
	parts := make([]string, 0, len(l.Preludes)+1)

	for _, ref := range l.Preludes {
		text, err := carriedText(doc.Preludes[ref].Rule, doc.Preludes[ref].Artifact, artifactPaths)
		if err != nil {
			return "", fmt.Errorf("prelude %q: %w", ref, err)
		}

		parts = append(parts, text)
	}

	text, err := carriedText(l.Rule, l.RuleArtifact, artifactPaths)
	if err != nil {
		return "", err
	}

	return strings.Join(append(parts, text), "\n"), nil
}

// carriedText returns inline text, or the content of the named artifact.
func carriedText(inline, artifact string, artifactPaths map[string]string) (string, error) {
	if artifact == "" {
		return inline, nil
	}

	content, err := os.ReadFile(artifactPaths[artifact])
	if err != nil {
		return "", fmt.Errorf("reading artifact %q: %w", artifact, err)
	}

	return string(content), nil
}

// writeRule stores an assembled rule program under its content hash, so the
// path (and so the linter's cache key) changes exactly when any part of it
// does — a prelude edit included.
func (r Resolver) writeRule(rule string) (string, error) {
	sum := sha256.Sum256([]byte(rule))
	path := filepath.Join(r.CacheDir, "rules", hex.EncodeToString(sum[:]))

	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == rule {
			return path, nil
		}

		return "", fmt.Errorf("%w: %s", ErrCachedCorrupt, path)
	}

	if err := writeFileAtomic(path, []byte(rule), 0o444); err != nil {
		return "", err
	}

	return path, nil
}

// materialize returns the cache path of a verified artifact, fetching it when
// absent. The directory is keyed by the pin, the file named by the artifact, so
// an executable is reachable by bare name once its directory is on PATH.
func (r Resolver) materialize(ctx context.Context, name string, a Artifact, id MarklID) (string, error) {
	dir := filepath.Join(r.CacheDir, "artifacts", id.Format+"-"+hex.EncodeToString(id.Digest))
	path := filepath.Join(dir, name)

	existing, err := os.ReadFile(path)

	switch {
	case err == nil:
		if verifyErr := id.Verify(existing); verifyErr != nil {
			return "", fmt.Errorf("artifact %q: %w at %s (remove it to refetch): %w",
				name, ErrCachedCorrupt, path, verifyErr)
		}

		return path, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("artifact %q: reading cache: %w", name, err)
	}

	content, err := r.fetch(ctx, a.URL)
	if err != nil {
		return "", fmt.Errorf("artifact %q: %w", name, err)
	}

	if err := id.Verify(content); err != nil {
		return "", fmt.Errorf("artifact %q from %s: %w", name, a.URL, err)
	}

	mode := os.FileMode(0o444)
	if a.IsExecutable() {
		mode = 0o555
	}

	if err := writeFileAtomic(path, content, mode); err != nil {
		return "", fmt.Errorf("artifact %q: %w", name, err)
	}

	return path, nil
}

func (r Resolver) fetch(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: parsing url %q: %w", ErrFetch, raw, err)
	}

	switch u.Scheme {
	case "file":
		content, readErr := os.ReadFile(u.Path)
		if readErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrFetch, readErr)
		}

		return content, nil
	case "https":
		return r.fetchHTTPS(ctx, u.String())
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedScheme, raw)
	}
}

func (r Resolver) fetchHTTPS(ctx context.Context, rawURL string) ([]byte, error) {
	client := http.Client{Timeout: 5 * time.Minute}
	if r.Client != nil {
		client = *r.Client
	}

	client.CheckRedirect = refuseLoginRedirects

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}

	content, readErr := io.ReadAll(io.LimitReader(resp.Body, maxArtifactBytes+1))
	closeErr := resp.Body.Close()

	switch {
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: %s: %s", ErrFetch, rawURL, resp.Status)
	case readErr != nil:
		return nil, fmt.Errorf("%w: reading %s: %w", ErrFetch, rawURL, readErr)
	case closeErr != nil:
		return nil, fmt.Errorf("%w: closing %s: %w", ErrFetch, rawURL, closeErr)
	case len(content) > maxArtifactBytes:
		return nil, fmt.Errorf("%w: %s", ErrArtifactTooLarge, rawURL)
	}

	return content, nil
}

// refuseLoginRedirects stops an artifact fetch that is being sent to log in.
// A forge that hides release downloads behind authentication answers an
// anonymous GET with a redirect to its login page, which may hop on to a
// localhost authorize endpoint. Following that fails anyway, but as a
// connection error to localhost that reads like a proxy problem. Refusing the
// hop names the actual cause. A downgrade to plain http is refused too.
func refuseLoginRedirects(req *http.Request, via []*http.Request) error {
	const maxRedirects = 10

	switch host := req.URL.Hostname(); {
	case len(via) >= maxRedirects:
		return fmt.Errorf("%w: stopped after %d redirects", ErrFetch, maxRedirects)
	case req.URL.Scheme != "https":
		return fmt.Errorf("%w: %s redirected to non-https %s", ErrArtifactNeedsLogin, via[0].URL, req.URL)
	case (host == "localhost" || isLoopback(host)) && req.URL.Host != via[0].URL.Host:
		// A hop onto a different loopback endpoint (a local authorize
		// callback). A server that itself lives on loopback may still redirect
		// within its own host:port.
		return fmt.Errorf("%w: %s redirected to %s", ErrArtifactNeedsLogin, via[0].URL, req.URL)
	case strings.Contains(req.URL.Path, "/login") || strings.HasPrefix(req.URL.Path, "/auth/"):
		return fmt.Errorf("%w: %s redirected to %s", ErrArtifactNeedsLogin, via[0].URL, req.URL)
	}

	return nil
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// writeFileAtomic writes content beside path and renames it into place, so a
// reader never observes a partial artifact.
func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	tmpName := tmp.Name()

	_, writeErr := tmp.Write(content)
	closeErr := tmp.Close()

	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tmpName)

		return fmt.Errorf("writing %s: %w", tmpName, err)
	}

	if err := os.Chmod(tmpName, mode); err != nil {
		_ = os.Remove(tmpName)

		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)

		return fmt.Errorf("installing %s: %w", path, err)
	}

	return nil
}
