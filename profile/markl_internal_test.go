package profile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseMarklIDRejectsNonDigestFormat needs the unexported encoder: a
// correctly checksummed payload whose format is a real markl format but not a
// content digest must be refused, not treated as unverifiable-but-fine.
func TestParseMarklIDRejectsNonDigestFormat(t *testing.T) {
	payload := blech32Encode("nonce", make([]byte, 32))

	_, err := ParseMarklID(PurposeArtifactDigest + "@" + payload)
	require.ErrorIs(t, err, ErrMarklUnsupportedFormat)
}

// TestParseMarklIDRejectsWrongDigestLength guards the length check: a digest
// format with a payload of the wrong size must not reach Verify.
func TestParseMarklIDRejectsWrongDigestLength(t *testing.T) {
	payload := blech32Encode("blake2b256", make([]byte, 20))

	_, err := ParseMarklID(PurposeArtifactDigest + "@" + payload)
	require.ErrorIs(t, err, ErrMarklDigestLength)
}
