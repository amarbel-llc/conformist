package profile_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/profile"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// serveProfile starts a TLS server whose /artifact path is handled by artifact,
// and returns a document pinning content at that url plus a resolver using the
// server's client.
func serveProfile(t *test_ui.T, content []byte, artifact http.HandlerFunc) (*profile.Document, profile.Resolver) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/artifact", artifact)
	mux.HandleFunc("/public", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(content) })
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>log in</html>")) })

	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)

	src := strings.Replace(validProfile, `url = "file:///unused"`, fmt.Sprintf("url = %q", server.URL+"/artifact"), 1)
	src = strings.Replace(src, `markl = "unused"`, fmt.Sprintf("markl = %q", profile.NewMarklID("sha256", content)), 1)

	doc, err := profile.Parse("test.profile", []byte(src))
	require.NoError(t, err)

	return doc, profile.Resolver{CacheDir: filepath.Join(t.TempDir(), "cache"), Client: server.Client()}
}

func TestResolveRefusesLoginRedirects(tt *testing.T) {
	content := []byte("#!/bin/sh\n")

	for name, target := range map[string]string{
		"login page":           "/auth/login?rd=/artifact",
		"localhost authorize":  "https://localhost:9098/authorize",
		"loopback ip":          "https://127.0.0.1:9098/authorize",
		"downgrade to http":    "http://example.invalid/artifact",
		"any path with /login": "/user/login",
	} {
		tt.Run(name, func(tt *testing.T) {
			t := &test_ui.T{T: tt}

			doc, r := serveProfile(t, content, func(w http.ResponseWriter, req *http.Request) {
				http.Redirect(w, req, target, http.StatusFound)
			})

			_, err := r.Resolve(t.Context(), doc)
			require.ErrorIs(t, err, profile.ErrArtifactNeedsLogin)
		})
	}
}

// TestResolveFollowsOrdinaryRedirects is the positive control: a redirect that
// is not to a login keeps working, so the refusal is not just "any redirect".
func TestResolveFollowsOrdinaryRedirects(tt *testing.T) {
	t := &test_ui.T{T: tt}
	content := []byte("#!/bin/sh\n")

	doc, r := serveProfile(t, content, func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/public", http.StatusFound)
	})

	_, err := r.Resolve(t.Context(), doc)
	require.NoError(t, err)
}
