package profile

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// sigVector is papi's RFC-0001 §15 conformance vector, copied verbatim from
// papi docs/rfcs/vectors/rfc0001-s15-hyphence-sig-v1.json (papi e774295). papi
// runs the same four checks against its own signer, so passing here means a
// profile papi signs verifies in conformist byte-for-byte.
type sigVector struct {
	Purpose                           string `json:"purpose"`
	PublicKey                         string `json:"public_key"`
	UnsignedDocument                  string `json:"unsigned_document"`
	UnsignedDocumentNoncanonicalOrder string `json:"unsigned_document_noncanonical_order"`
	SignedInputHex                    string `json:"signed_input_hex"`
	SignedDocument                    string `json:"signed_document"`
}

func TestPAPISignatureVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/rfc0001-s15-hyphence-sig-v1.json")
	require.NoError(t, err)

	var v sigVector
	require.NoError(t, json.Unmarshal(raw, &v))
	require.Equal(t, SignaturePurpose, v.Purpose)

	want, err := hex.DecodeString(v.SignedInputHex)
	require.NoError(t, err)

	for name, doc := range map[string]string{
		"unsigned_document":                    v.UnsignedDocument,
		"unsigned_document_noncanonical_order": v.UnsignedDocumentNoncanonicalOrder,
		"signed_document":                      v.SignedDocument,
	} {
		t.Run("signed input of "+name, func(t *testing.T) {
			lines, body, err := readHyphence([]byte(doc))
			require.NoError(t, err)

			got, err := SignedInput(lines, body)
			require.NoError(t, err)
			require.Equal(t, string(want), string(got))
		})
	}

	t.Run("signed_document verifies against public_key", func(t *testing.T) {
		got, err := VerifySignature([]byte(v.SignedDocument), []string{v.PublicKey})
		require.NoError(t, err)
		require.Equal(t, v.PublicKey, got)
	})
}
