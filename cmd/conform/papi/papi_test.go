package papi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/cmd/conform/papi"
	"github.com/stretchr/testify/require"
)

// papiServer stands in for a domain's PAPI surface: a discovery document at
// /.well-known/papi advertising resources.templates, and the templates
// collection at /papi/templates. Both use the universal {data, meta} envelope.
// It reports its own host in the advertised URL so same-host restriction passes.
func papiServer(t *testing.T, templatesJSON string, advertiseTemplates bool) *httptest.Server { //testui:allow // testify helper
	t.Helper()

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// srv.URL is http://127.0.0.1:PORT; the resolver builds https://<host>, so
	// the host (with port) is what must match for same-host acceptance. Tests
	// drive the resolver at the httptest host directly (see newResolver).
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/.well-known/papi", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		templatesURL := ""
		if advertiseTemplates {
			templatesURL = "https://" + host + "/papi/templates"
		}
		_, _ = w.Write([]byte(`{"data":{"resources":{"templates":"` + templatesURL + `"}},"meta":{"type":"papi-discovery"}}`))
	})

	mux.HandleFunc("/papi/templates", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(templatesJSON))
	})

	return srv
}

// newResolver returns a resolver whose client rewrites the https scheme (which
// the resolver hard-codes) back to the httptest server's http, so tests need no
// TLS. The Host is preserved, so same-host restriction is exercised for real.
func newResolver(srv *httptest.Server) (papi.Resolver, string) {
	host := strings.TrimPrefix(srv.URL, "http://")
	rt := rewriteTransport{host: host}

	return papi.Resolver{Client: &http.Client{Transport: rt}}, host
}

type rewriteTransport struct {
	host string
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// The resolver only ever issues https://<host>/... requests; send them to
	// the httptest server over http instead.
	req.URL.Scheme = "http"
	req.URL.Host = rt.host

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("rewrite transport: %w", err)
	}

	return resp, nil
}

const twoTemplates = `{
  "data": [
    {"id":"eng","flakeref":"github:amarbel-llc/conformist#eng","description":"eng repo","kind":"flake-template"},
    {"id":"lib","flakeref":"github:amarbel-llc/conformist#lib","description":"library repo"}
  ],
  "meta": {"count": 2}
}`

func TestResolveReturnsVisibleTemplates(t *testing.T) {
	srv := papiServer(t, twoTemplates, true)
	r, host := newResolver(srv)

	got, err := r.Resolve(context.Background(), host)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "eng", got[0].ID)
	require.Equal(t, "github:amarbel-llc/conformist#eng", got[0].Flakeref)
	// An omitted kind defaults to flake-template and is kept.
	require.Equal(t, "lib", got[1].ID)
}

// TestResolveFallsBackWhenTemplatesKeyAbsent verifies the discovery doc need not
// advertise resources.templates: the resolver falls back to <base>/papi/templates.
func TestResolveFallsBackWhenTemplatesKeyAbsent(t *testing.T) {
	srv := papiServer(t, twoTemplates, false)
	r, host := newResolver(srv)

	got, err := r.Resolve(context.Background(), host)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

// TestResolveSkipsUnknownKindsAndIncomplete verifies entries with an
// unrecognized kind or a missing id/flakeref are dropped (§8.1).
func TestResolveSkipsUnknownKindsAndIncomplete(t *testing.T) {
	const mixed = `{"data":[
	  {"id":"eng","flakeref":"github:x#eng","kind":"flake-template"},
	  {"id":"oci","flakeref":"ghcr.io/x","kind":"oci-image"},
	  {"id":"","flakeref":"github:x#anon"},
	  {"id":"nolink","flakeref":"","kind":"flake-template"}
	],"meta":{}}`
	srv := papiServer(t, mixed, true)
	r, host := newResolver(srv)

	got, err := r.Resolve(context.Background(), host)
	require.NoError(t, err)
	require.Equal(t, []string{"eng"}, papi.IDs(got), "only the understood, complete flake-template survives")
}

// TestResolveIgnoresForeignHostTemplatesURL verifies an advertised templates URL
// on a different host is ignored in favor of the same-host fallback (the
// resolution-restricted-to-domain security property). The server advertises a
// foreign URL but still serves /papi/templates, which is what must be used.
func TestResolveIgnoresForeignHostTemplatesURL(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/.well-known/papi", func(w http.ResponseWriter, _ *http.Request) {
		// Advertise an evil off-host collection URL.
		_, _ = w.Write([]byte(`{"data":{"resources":{"templates":"https://evil.example.com/steal"}},"meta":{}}`))
	})
	mux.HandleFunc("/papi/templates", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"eng","flakeref":"github:x#eng"}],"meta":{}}`))
	})

	r := papi.Resolver{Client: &http.Client{Transport: rewriteTransport{host: host}}}
	got, err := r.Resolve(context.Background(), host)
	require.NoError(t, err)
	require.Equal(t, []string{"eng"}, papi.IDs(got), "must use the same-host fallback, not the foreign URL")
}

// TestResolveFollowsSubdomainTemplatesURL verifies an advertised templates URL
// on a SUBDOMAIN of the operator-named host is followed (the real
// linenisgreat.com → api.linenisgreat.com delegation), not rejected. It is
// distinguished from the same-host fallback by serving the real collection at a
// non-default path that only the advertised URL points to.
func TestResolveFollowsSubdomainTemplatesURL(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	mux.HandleFunc("/.well-known/papi", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"resources":{"templates":"https://api.` + host + `/collections/templates"}},"meta":{}}`))
	})
	mux.HandleFunc("/collections/templates", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"sub","flakeref":"github:x#sub"}],"meta":{}}`))
	})
	// If the resolver wrongly fell back to the same-host path, it would see this.
	mux.HandleFunc("/papi/templates", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"fallback","flakeref":"github:x#fallback"}],"meta":{}}`))
	})

	r := papi.Resolver{Client: &http.Client{Transport: rewriteTransport{host: host}}}
	got, err := r.Resolve(context.Background(), host)
	require.NoError(t, err)
	require.Equal(t, []string{"sub"}, papi.IDs(got), "must follow the subdomain-delegated URL, not the fallback")
}

func TestSelectByID(t *testing.T) {
	ts := []papi.Template{
		{ID: "eng", Flakeref: "github:x#eng"},
		{ID: "lib", Flakeref: "github:x#lib"},
	}

	got, err := papi.Select(ts, "lib", nil)
	require.NoError(t, err)
	require.Equal(t, "lib", got.ID)
}

func TestSelectByIDNoMatchIsErrorWithNoFallback(t *testing.T) {
	ts := []papi.Template{{ID: "eng", Flakeref: "github:x#eng"}}

	_, err := papi.Select(ts, "nope", nil)
	require.ErrorIs(t, err, papi.ErrNoMatch)
	require.Contains(t, err.Error(), "eng", "diagnostic lists the available ids")
}

func TestSelectBareSingleUsesIt(t *testing.T) {
	ts := []papi.Template{{ID: "only", Flakeref: "github:x#only"}}

	got, err := papi.Select(ts, "", nil)
	require.NoError(t, err)
	require.Equal(t, "only", got.ID)
}

func TestSelectBareMultipleNonInteractiveFails(t *testing.T) {
	ts := []papi.Template{
		{ID: "eng", Flakeref: "github:x#eng"},
		{ID: "lib", Flakeref: "github:x#lib"},
	}

	_, err := papi.Select(ts, "", nil)
	require.ErrorIs(t, err, papi.ErrAmbiguous)
	require.Contains(t, err.Error(), "eng")
	require.Contains(t, err.Error(), "lib")
}

func TestSelectBareMultipleInteractiveUsesChooser(t *testing.T) {
	ts := []papi.Template{
		{ID: "eng", Flakeref: "github:x#eng"},
		{ID: "lib", Flakeref: "github:x#lib"},
	}

	got, err := papi.Select(ts, "", func(in []papi.Template) (papi.Template, error) {
		return in[1], nil
	})
	require.NoError(t, err)
	require.Equal(t, "lib", got.ID)
}

func TestSelectEmptyIsNoTemplates(t *testing.T) {
	_, err := papi.Select(nil, "", nil)
	require.ErrorIs(t, err, papi.ErrNoTemplates)

	_, err = papi.Select(nil, "eng", nil)
	require.ErrorIs(t, err, papi.ErrNoTemplates)
}

// TestSelectErrorsThreadWrap confirms the wrapped diagnostic still matches via
// errors.Is (used by the cmd layer's exit-code mapping).
func TestSelectErrorsThreadWrap(t *testing.T) {
	_, err := papi.Select([]papi.Template{{ID: "a", Flakeref: "f"}}, "b", nil)
	require.ErrorIs(t, err, papi.ErrNoMatch)
}

func TestResolvePropagatesHTTPError(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	mux.HandleFunc("/.well-known/papi", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})

	r := papi.Resolver{Client: &http.Client{Transport: rewriteTransport{host: host}}}
	_, err := r.Resolve(context.Background(), host)
	require.Error(t, err)
	require.Contains(t, err.Error(), "discovery document")
}

func TestSplitTarget(t *testing.T) {
	cases := []struct {
		in         string
		wantDomain string
		wantID     string
	}{
		{"example.com", "example.com", ""},
		{"example.com#eng", "example.com", "eng"},
		{"https://example.com#eng", "example.com", "eng"},
		{"https://example.com/#eng", "example.com", "eng"},
		{"api.example.com/path#id", "api.example.com", "id"},
		{"  example.com # eng ", "example.com", "eng"},
	}
	for _, c := range cases {
		d, id := papi.SplitTarget(c.in)
		require.Equal(t, c.wantDomain, d, "domain for %q", c.in)
		require.Equal(t, c.wantID, id, "id for %q", c.in)
	}
}
