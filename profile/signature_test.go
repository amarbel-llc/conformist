package profile

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code.linenisgreat.com/hyphence/go/hyphence"
	"github.com/stretchr/testify/require"
)

// signedTestProfile carries every metadata kind the signed input must handle:
// a description, a leading `%` comment, and a body line with an apostrophe.
const signedTestProfile = `---
# a signed test profile
% a leading comment on the type line
! toml-conformist_profile-v1
---

[artifact.tool]
form = "static"
url = "file:///unused"
markl = "unused"

[linter.recipes]
command = "tool --dump"
rule-tool = "jq"
includes = ["justfile"]
passes-files = false
rule = '''
.[] | "it's '\(.)'"
'''
`

func newTestSigningKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	uncompressed, err := key.PublicKey.Bytes() // 0x04 ‖ X ‖ Y
	require.NoError(t, err)

	compressed := append([]byte{0x02 | uncompressed[len(uncompressed)-1]&1}, uncompressed[1:1+p256CoordLen]...)

	return key, purposePIVAuth + "@" + blech32Encode(formatSSHEcdsaP256, compressed)
}

// signForTest signs data the way `papi hyphence sign` does: the signature line
// goes immediately before the `!` line and the document is re-emitted
// canonically.
func signForTest(t *testing.T, key *ecdsa.PrivateKey, data string) string {
	t.Helper()

	lines, body, err := readHyphence([]byte(data))
	require.NoError(t, err)

	input, err := SignedInput(lines, body)
	require.NoError(t, err)

	digest := sha256.Sum256(input)
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	require.NoError(t, err)

	sig := make([]byte, ecdsaP256SigLen)
	r.FillBytes(sig[:p256CoordLen])
	s.FillBytes(sig[p256CoordLen:])

	sigLine := hyphence.MetadataLine{Prefix: '-', Value: SignaturePurpose + "@" + blech32Encode(formatEcdsaP256Sig, sig)}

	signed := make([]hyphence.MetadataLine, 0, len(lines)+1)

	for _, l := range lines {
		if l.Prefix == '!' {
			signed = append(signed, sigLine)
		}

		signed = append(signed, l)
	}

	var buf bytes.Buffer

	emitter := &hyphence.FormatBodyEmitter{Doc: &hyphence.Document{Metadata: signed}, Out: &buf}
	_, err = emitter.ReadFrom(bytes.NewReader(body))
	require.NoError(t, err)

	return buf.String()
}

func TestVerifySignature(t *testing.T) {
	key, keyID := newTestSigningKey(t)
	_, otherKeyID := newTestSigningKey(t)
	signed := signForTest(t, key, signedTestProfile)

	t.Run("valid", func(t *testing.T) {
		got, err := VerifySignature([]byte(signed), []string{otherKeyID, keyID})
		require.NoError(t, err)
		require.Equal(t, keyID, got, "reports the key that verified")
	})

	t.Run("a signed profile still parses", func(t *testing.T) {
		_, err := Parse("signed.profile", []byte(signed))
		require.NoError(t, err)
	})

	// Metadata order does not change what is signed (hyphence canonical form).
	t.Run("reordered metadata still verifies", func(t *testing.T) {
		reordered := strings.Replace(signed, "# a signed test profile\n", "", 1)
		reordered = strings.Replace(reordered, "! toml-conformist_profile-v1\n",
			"! toml-conformist_profile-v1\n# a signed test profile\n", 1)
		require.NotEqual(t, signed, reordered)

		_, err := VerifySignature([]byte(reordered), []string{keyID})
		require.NoError(t, err)
	})

	for name, tamper := range map[string][2]string{
		"body changed":           {`command = "tool --dump"`, `command = "evil --dump"`},
		"description changed":    {"# a signed test profile", "# a forged test profile"},
		"leading comment change": {"% a leading comment", "% a different comment"},
	} {
		t.Run(name+" fails", func(t *testing.T) {
			changed := strings.Replace(signed, tamper[0], tamper[1], 1)
			require.NotEqual(t, signed, changed, "the case must change the document")

			_, err := VerifySignature([]byte(changed), []string{keyID})
			require.ErrorIs(t, err, ErrSignatureInvalid)
		})
	}

	for name, tc := range map[string]struct {
		data    string
		trusted []string
		want    error
	}{
		"untrusted key":   {signed, []string{otherKeyID}, ErrSignatureInvalid},
		"unsigned":        {signedTestProfile, []string{keyID}, ErrUnsigned},
		"no trusted keys": {signed, nil, ErrNoTrustedKey},
		"malformed key":   {signed, []string{"piggy-piv_auth-v1@nonsense"}, ErrMalformedKey},
		"two signature lines": {
			strings.Replace(signed, "! toml", fmt.Sprintf("- %s@x\n! toml", SignaturePurpose), 1),
			[]string{keyID},
			ErrMultipleSignatures,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := VerifySignature([]byte(tc.data), tc.trusted)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// A truncated profile — metadata with no closing boundary — must be refused as
// not-hyphence, not half-read.
func TestParseRejectsUnclosedMetadata(t *testing.T) {
	_, err := Parse("truncated.profile", []byte("---\n# truncated\n! toml-conformist_profile-v1\n"))
	require.ErrorIs(t, err, ErrNotHyphence)
}

func TestParseStillRejectsDelegationLines(t *testing.T) {
	src := strings.Replace(signedTestProfile, "! toml", "- baseline=x\n! toml", 1)

	_, err := Parse("test.profile", []byte(src))
	require.ErrorIs(t, err, ErrUnsupportedInPOC)
}

func TestPublishedSigningKeys(t *testing.T) {
	_, a := newTestSigningKey(t)
	_, b := newTestSigningKey(t)

	body := "# piggy ids\n" + a + "  # primary yubikey\n\npiggy-recipient-v1@x\n" + b + "\n"
	require.Equal(t, []string{a, b}, PublishedSigningKeys([]byte(body)))
}

// servePAPI serves a profile and a piggy-ids list the way a PAPI domain does.
func servePAPI(t *testing.T, profileBody, piggyIDs string) (*httptest.Server, Resolver) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/papi/conformist-profile", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(profileBody))
	})
	mux.HandleFunc("/papi/piggy-ids", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(piggyIDs))
	})

	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)

	return server, Resolver{CacheDir: t.TempDir(), Client: server.Client()}
}

func TestFetchSignedProfile(t *testing.T) {
	key, keyID := newTestSigningKey(t)
	_, otherKeyID := newTestSigningKey(t)
	signed := signForTest(t, key, signedTestProfile)

	t.Run("pinned and published key verifies", func(t *testing.T) {
		server, r := servePAPI(t, signed, keyID+"\n")

		data, got, err := r.FetchSignedProfile(t.Context(), server.URL+"/papi/conformist-profile", []string{keyID})
		require.NoError(t, err)
		require.Equal(t, keyID, got)
		require.Equal(t, signed, string(data))
	})

	// A domain that stops publishing a key withdraws it, even from consumers
	// who still pin it.
	t.Run("pinned but not published is refused", func(t *testing.T) {
		server, r := servePAPI(t, signed, otherKeyID+"\n")

		_, _, err := r.FetchSignedProfile(t.Context(), server.URL+"/papi/conformist-profile", []string{keyID})
		require.ErrorIs(t, err, ErrKeyNotPublished)
	})

	// Publication alone is not trust: an unpinned key is refused even though
	// it verifies and the domain publishes it.
	t.Run("published but not pinned is refused", func(t *testing.T) {
		server, r := servePAPI(t, signed, keyID+"\n")

		_, _, err := r.FetchSignedProfile(t.Context(), server.URL+"/papi/conformist-profile", []string{otherKeyID})
		require.ErrorIs(t, err, ErrKeyNotPublished)
	})

	t.Run("unsigned profile is refused", func(t *testing.T) {
		server, r := servePAPI(t, signedTestProfile, keyID+"\n")

		_, _, err := r.FetchSignedProfile(t.Context(), server.URL+"/papi/conformist-profile", []string{keyID})
		require.ErrorIs(t, err, ErrUnsigned)
	})

	t.Run("no pinned key is refused before fetching", func(t *testing.T) {
		_, _, err := Resolver{}.FetchSignedProfile(t.Context(), "https://unused.invalid/papi/conformist-profile", nil)
		require.ErrorIs(t, err, ErrNoTrustedKey)
	})

	t.Run("plain http is refused", func(t *testing.T) {
		_, _, err := Resolver{}.FetchSignedProfile(t.Context(), "http://unused.invalid/p", []string{keyID})
		require.ErrorIs(t, err, ErrUnsupportedScheme)
	})
}
