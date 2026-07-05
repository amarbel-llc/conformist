package conform_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/conformist/cmd/conform"
	"github.com/amarbel-llc/conformist/cmd/conform/papi"
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

func TestBootstrapNoTemplatesIsError(t *testing.T) {
	r, host := fixtureResolver(t, `{"data":[],"meta":{}}`)

	err := conform.Bootstrap(context.Background(), host, t.TempDir(), &bytes.Buffer{}, conform.BootstrapOptions{
		Resolver:  r,
		FlakeInit: failInit(t),
	})
	require.ErrorIs(t, err, papi.ErrNoTemplates)
}
