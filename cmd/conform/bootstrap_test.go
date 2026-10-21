package conform_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/cmd/conform"
	"code.linenisgreat.com/conformist/cmd/conform/papi"
	"github.com/stretchr/testify/require"
)

// httpsToHTTP sends the resolver's hard-coded https://<host>/... requests to the
// httptest server over http, preserving Host so no TLS is needed in tests.
type httpsToHTTP struct{ host string }

func (t httpsToHTTP) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.host

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("rewrite transport: %w", err)
	}

	return resp, nil
}

// fixtureResolver stands up a PAPI surface serving templatesJSON and returns a
// resolver wired to it plus the host to pass as the domain.
func fixtureResolver(t *testing.T, templatesJSON string) (papi.Resolver, string) { //testui:allow // testify helper
	t.Helper()

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/.well-known/papi", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"resources":{"templates":"https://` + host + `/papi/templates"}},"meta":{}}`))
	})
	mux.HandleFunc("/papi/templates", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(templatesJSON))
	})

	return papi.Resolver{Client: &http.Client{Transport: httpsToHTTP{host: host}}}, host
}

// failInit is a FlakeInit stub that fails the test if called.
func failInit(t *testing.T) func(context.Context, string, string) error { //testui:allow // testify helper
	t.Helper()

	return func(context.Context, string, string) error {
		t.Fatal("nix flake init must not run")

		return nil
	}
}

func TestBootstrapSingleTemplateInits(t *testing.T) {
	r, host := fixtureResolver(t,
		`{"data":[{"id":"eng","flakeref":"github:amarbel-llc/conformist#eng","description":"eng repo","kind":"flake-template"}],"meta":{}}`)
	dir := t.TempDir()

	var gotDir, gotRef string
	var out bytes.Buffer
	err := conform.Bootstrap(context.Background(), host, dir, &out, conform.BootstrapOptions{
		Resolver: r,
		NoLock:   true,
		FlakeInit: func(_ context.Context, d, ref string) error {
			gotDir, gotRef = d, ref

			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, "github:amarbel-llc/conformist#eng", gotRef)
	require.Equal(t, dir, gotDir)
	require.Contains(t, out.String(), "github:amarbel-llc/conformist#eng", "the resolved flakeref is surfaced before init")
}

func TestBootstrapByID(t *testing.T) {
	r, host := fixtureResolver(t, `{"data":[
	  {"id":"eng","flakeref":"github:x#eng"},
	  {"id":"lib","flakeref":"github:x#lib"}],"meta":{}}`)

	var gotRef string
	err := conform.Bootstrap(context.Background(), host+"#lib", t.TempDir(), &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver: r,
		NoLock:   true,
		FlakeInit: func(_ context.Context, _, ref string) error {
			gotRef = ref

			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, "github:x#lib", gotRef)
}

func TestBootstrapAmbiguousNonInteractiveFails(t *testing.T) {
	r, host := fixtureResolver(t, `{"data":[
	  {"id":"eng","flakeref":"github:x#eng"},
	  {"id":"lib","flakeref":"github:x#lib"}],"meta":{}}`)

	err := conform.Bootstrap(context.Background(), host, t.TempDir(), &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:    r,
		Interactive: false,
		FlakeInit:   failInit(t),
	})
	require.ErrorIs(t, err, papi.ErrAmbiguous)
}

func TestBootstrapRefusesNonEmptyDir(t *testing.T) {
	r, host := fixtureResolver(t, `{"data":[{"id":"eng","flakeref":"github:x#eng"}],"meta":{}}`)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("x"), 0o644))

	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:  r,
		FlakeInit: failInit(t),
	})
	require.ErrorIs(t, err, conform.ErrTargetNotEmpty)
}

func TestBootstrapOverwriteAllowsNonEmpty(t *testing.T) {
	r, host := fixtureResolver(t, `{"data":[{"id":"eng","flakeref":"github:x#eng"}],"meta":{}}`)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("x"), 0o644))

	ran := false
	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:  r,
		Overwrite: true,
		NoLock:    true,
		FlakeInit: func(_ context.Context, _, _ string) error {
			ran = true

			return nil
		},
	})
	require.NoError(t, err)
	require.True(t, ran)
}

// TestBootstrapGitOnlyDirIsEmpty verifies a freshly `git init`ed dir (only .git)
// still counts as empty, so `git init && conform <domain>` works without
// --overwrite.
func TestBootstrapGitOnlyDirIsEmpty(t *testing.T) {
	r, host := fixtureResolver(t, `{"data":[{"id":"eng","flakeref":"github:x#eng"}],"meta":{}}`)
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	ran := false
	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver: r,
		FlakeInit: func(_ context.Context, _, _ string) error {
			ran = true

			return nil
		},
	})
	require.NoError(t, err)
	require.True(t, ran, "a dir containing only .git counts as empty for bootstrap")
}

// templateFiles is what the finish-step tests have the FlakeInit stub write: a
// version.env and conformist.nix carrying the placeholder, plus an unrelated file.
func templateFiles(t *testing.T) func(context.Context, string, string) error { //testui:allow // testify helper
	t.Helper()

	return func(_ context.Context, dir, _ string) error {
		for name, content := range map[string]string{
			"version.env":    "export EXAMPLE_VERSION=0.1.0\n",
			"conformist.nix": "{ linters.eng-versioning.key = \"EXAMPLE_VERSION\"; }\n",
			"justfile":       "default:\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", name, err)
			}
		}

		return nil
	}
}

const singleTemplate = `{"data":[{"id":"eng","flakeref":"github:x#eng"}],"meta":{}}`

func TestBootstrapSetsVersionKeyFromDirName(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := filepath.Join(t.TempDir(), "my-repo")
	require.NoError(t, os.Mkdir(dir, 0o755))

	var out bytes.Buffer
	err := conform.Bootstrap(context.Background(), host, dir, &out, conform.BootstrapOptions{
		Resolver:  r,
		NoLock:    true,
		FlakeInit: templateFiles(t),
	})
	require.NoError(t, err)

	versionEnv, err := os.ReadFile(filepath.Join(dir, "version.env"))
	require.NoError(t, err)
	require.Equal(t, "export MY_REPO_VERSION=0.1.0\n", string(versionEnv))

	nix, err := os.ReadFile(filepath.Join(dir, "conformist.nix"))
	require.NoError(t, err)
	require.Contains(t, string(nix), `"MY_REPO_VERSION"`)
	require.NotContains(t, string(nix), "EXAMPLE_VERSION")
	require.Contains(t, out.String(), "set MY_REPO_VERSION in version.env, conformist.nix")
}

// TestBootstrapSetsCrateName verifies the Rust package templates' crate-name
// placeholder is filled in Cargo.toml and AGENTS.md, from --name or the dir name.
func TestBootstrapSetsCrateName(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()

	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver: r,
		Name:     "My_Repo.rs",
		NoLock:   true,
		FlakeInit: func(_ context.Context, d, _ string) error {
			for name, content := range map[string]string{
				"Cargo.toml": "[package]\nname = \"EXAMPLE_CRATE\"\n",
				"AGENTS.md":  "# EXAMPLE_CRATE\n",
				"README.md":  "EXAMPLE_CRATE untouched\n",
			} {
				if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o644); err != nil {
					return fmt.Errorf("writing %s: %w", name, err)
				}
			}

			return nil
		},
	})
	require.NoError(t, err)

	cargo, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	require.NoError(t, err)
	require.Equal(t, "[package]\nname = \"my_repo-rs\"\n", string(cargo))

	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	require.NoError(t, err)
	require.Equal(t, "# my_repo-rs\n", string(agents))

	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	require.NoError(t, err)
	require.Contains(t, string(readme), "EXAMPLE_CRATE", "only the named files are substituted")
}

func TestBootstrapNameFlagOverridesVersionKey(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()

	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:  r,
		Name:      "prophit",
		NoLock:    true,
		FlakeInit: templateFiles(t),
	})
	require.NoError(t, err)

	versionEnv, err := os.ReadFile(filepath.Join(dir, "version.env"))
	require.NoError(t, err)
	require.Equal(t, "export PROPHIT_VERSION=0.1.0\n", string(versionEnv))
}

// TestBootstrapLeavesPreexistingKeyedFileAlone verifies --overwrite does not
// rewrite a version.env the user already had when the template did not touch it.
func TestBootstrapLeavesPreexistingKeyedFileAlone(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()
	const mine = "export EXAMPLE_VERSION=9.9.9\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "version.env"), []byte(mine), 0o644))

	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:  r,
		Overwrite: true,
		NoLock:    true,
		FlakeInit: func(_ context.Context, _, _ string) error { return nil },
	})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "version.env"))
	require.NoError(t, err)
	require.Equal(t, mine, string(got), "a file the template did not write is never rewritten")
}

func TestBootstrapStagesTouchedFilesInGitRepoAndLocks(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	var staged [][]string
	locked := 0
	var out bytes.Buffer
	err := conform.Bootstrap(context.Background(), host, dir, &out, conform.BootstrapOptions{
		Resolver:  r,
		FlakeInit: templateFiles(t),
		GitStage: func(_ context.Context, _ string, paths []string) error {
			staged = append(staged, paths)

			return nil
		},
		FlakeLock: func(context.Context, string) error {
			locked++

			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, locked)
	require.Equal(t, [][]string{{"conformist.nix", "justfile", "version.env"}, {"flake.lock"}}, staged,
		"stage exactly the template's files, then flake.lock after locking; never a commit")
	require.Contains(t, out.String(), "locked flake inputs")
	require.Contains(t, out.String(), "Remaining: create .envrc containing `use flake` unless spinclass manages it, "+
		"then `direnv allow`; review, then commit.")
}

func TestBootstrapNoLockSkipsLockAndSaysSo(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	var out bytes.Buffer
	err := conform.Bootstrap(context.Background(), host, dir, &out, conform.BootstrapOptions{
		Resolver:  r,
		NoLock:    true,
		FlakeInit: templateFiles(t),
		GitStage:  func(context.Context, string, []string) error { return nil },
		FlakeLock: func(context.Context, string) error {
			t.Fatal("nix flake lock must not run with NoLock")

			return nil
		},
	})
	require.NoError(t, err)
	require.Contains(t, out.String(), "Remaining: `nix flake lock`; create .envrc containing `use flake`")
}

// TestBootstrapLockFailureWarnsButSucceeds verifies a failed lock does not fail
// the bootstrap: the template is already in place.
func TestBootstrapLockFailureWarnsButSucceeds(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	var out bytes.Buffer
	err := conform.Bootstrap(context.Background(), host, dir, &out, conform.BootstrapOptions{
		Resolver:  r,
		FlakeInit: templateFiles(t),
		GitStage:  func(context.Context, string, []string) error { return nil },
		FlakeLock: func(context.Context, string) error { return errors.New("offline") },
	})
	require.NoError(t, err)
	require.Contains(t, out.String(), "warning: nix flake lock failed: offline")
	require.Contains(t, out.String(), "Remaining: `nix flake lock`;")
}

// TestBootstrapStagesFilesTheRepoExcludes is the spinclass case (conformist#141):
// the repo's .git/info/exclude lists .envrc, and `nix flake init` (stubbed here
// the way it behaves: write the file, then add it with intent-to-add) leaves it
// in the index, so the real `git add` succeeds, the lock still runs and the
// bootstrap finishes clean.
func TestBootstrapStagesFilesTheRepoExcludes(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@code.linenisgreat.com:x.git")

	exclude := filepath.Join(dir, ".git", "info", "exclude")
	require.NoError(t, os.MkdirAll(filepath.Dir(exclude), 0o755))
	require.NoError(t, os.WriteFile(exclude, []byte(".envrc\n"), 0o644))

	locked := false
	var out bytes.Buffer
	err := conform.Bootstrap(context.Background(), host, dir, &out, conform.BootstrapOptions{
		Resolver: r,
		FlakeInit: func(ctx context.Context, d, _ string) error {
			for _, name := range []string{".envrc", "version.env"} {
				if err := os.WriteFile(filepath.Join(d, name), []byte("x\n"), 0o644); err != nil {
					return fmt.Errorf("writing %s: %w", name, err)
				}
			}

			// nix adds what it wrote even when an exclude pattern matches, so force.
			cmd := exec.CommandContext(ctx, "git", "-C", d, "add", "--intent-to-add", "--force", "--",
				".envrc", "version.env")
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("git add -N: %w: %s", err, out)
			}

			return nil
		},
		FlakeLock: func(_ context.Context, d string) error {
			locked = true

			return os.WriteFile(filepath.Join(d, "flake.lock"), []byte("{}\n"), 0o644)
		},
	})
	require.NoError(t, err)

	status, err := exec.CommandContext(context.Background(), "git", "-C", dir, "status", "--short").Output()
	require.NoError(t, err)
	require.Contains(t, string(status), "A  .envrc", "the excluded file is staged, not just intent-to-add")
	require.True(t, locked, "an excluded template file must not cost the lock step")
	require.Contains(t, out.String(), "staged 2 files")
	require.NotContains(t, out.String(), "warning")
}

// countingTemplate wraps templateFiles with a record of every directory the
// template was instantiated into.
func countingTemplate(t *testing.T, dirs *[]string) func(context.Context, string, string) error { //testui:allow // testify helper
	t.Helper()

	inner := templateFiles(t)

	return func(ctx context.Context, d, ref string) error {
		*dirs = append(*dirs, d)

		return inner(ctx, d, ref)
	}
}

// TestBootstrapConflictAbortsBeforeWriting is #142 scenario 2: one template file
// collides with a different existing file, so nothing may be written to the
// target and the error names the colliding file.
func TestBootstrapConflictAbortsBeforeWriting(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "justfile"), []byte("mine:\n"), 0o644))

	var dirs []string
	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:  r,
		Overwrite: true,
		NoLock:    true,
		FlakeInit: countingTemplate(t, &dirs),
	})
	require.ErrorIs(t, err, conform.ErrTemplateConflict)
	require.Contains(t, err.Error(), "justfile")
	require.NotContains(t, err.Error(), "version.env", "only colliding files are listed")

	require.NotContains(t, dirs, dir, "the template is never instantiated into the target")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no partial tree: only the user's justfile remains")

	got, err := os.ReadFile(filepath.Join(dir, "justfile"))
	require.NoError(t, err)
	require.Equal(t, "mine:\n", string(got))
}

// TestBootstrapRerunFillsPlaceholdersOfIdenticalFiles is #142 scenario 3: every
// template file already sits in the target byte-for-byte (nix would skip them
// all), yet the placeholders are still filled and the files staged.
func TestBootstrapRerunFillsPlaceholdersOfIdenticalFiles(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, templateFiles(t)(context.Background(), dir, ""))

	// nix flake init writes nothing when everything is identical.
	var staged [][]string
	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:  r,
		Overwrite: true,
		Name:      "prophit",
		NoLock:    true,
		FlakeInit: func(_ context.Context, d, _ string) error {
			if d == dir {
				return nil
			}

			return templateFiles(t)(context.Background(), d, "")
		},
		GitStage: func(_ context.Context, _ string, paths []string) error {
			staged = append(staged, paths)

			return nil
		},
	})
	require.NoError(t, err)

	versionEnv, err := os.ReadFile(filepath.Join(dir, "version.env"))
	require.NoError(t, err)
	require.Equal(t, "export PROPHIT_VERSION=0.1.0\n", string(versionEnv))
	require.Equal(t, [][]string{{"conformist.nix", "justfile", "version.env"}}, staged)
}

// TestBootstrapNonCollidingExistingFilesProceed is the --overwrite happy path
// beside user files the template does not write: no conflict, template written.
func TestBootstrapNonCollidingExistingFilesProceed(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644))

	var dirs []string
	err := conform.Bootstrap(context.Background(), host, dir, &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:  r,
		Overwrite: true,
		Name:      "prophit",
		NoLock:    true,
		FlakeInit: countingTemplate(t, &dirs),
	})
	require.NoError(t, err)
	require.Contains(t, dirs, dir)

	versionEnv, err := os.ReadFile(filepath.Join(dir, "version.env"))
	require.NoError(t, err)
	require.Equal(t, "export PROPHIT_VERSION=0.1.0\n", string(versionEnv))
}

func TestBootstrapNonGitDirDoesNotStage(t *testing.T) {
	r, host := fixtureResolver(t, singleTemplate)

	var out bytes.Buffer
	err := conform.Bootstrap(context.Background(), host, t.TempDir(), &out, conform.BootstrapOptions{
		Resolver:  r,
		FlakeInit: templateFiles(t),
		GitStage: func(context.Context, string, []string) error {
			t.Fatal("must not stage outside a git repo")

			return nil
		},
		FlakeLock: func(context.Context, string) error { return nil },
	})
	require.NoError(t, err)
	require.Contains(t, out.String(), "`git init` and `git add` the scaffold")
}

func TestBootstrapNoTemplatesIsError(t *testing.T) {
	r, host := fixtureResolver(t, `{"data":[],"meta":{}}`)

	err := conform.Bootstrap(context.Background(), host, t.TempDir(), &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:  r,
		FlakeInit: failInit(t),
	})
	require.ErrorIs(t, err, papi.ErrNoTemplates)
}
