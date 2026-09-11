package profile

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// MarklID is a purpose-full markl-id, `purpose@format-payload` (piggy RFC
// 0011): the identifier an artifact pin is spelled as (RFC 0005 §2).
type MarklID struct {
	// Purpose names what the identifier is for, e.g. a content digest.
	Purpose string
	// Format is the payload's format id, e.g. `sha256`; it is the blech32 HRP.
	Format string
	// Digest is the decoded payload.
	Digest []byte
}

var (
	ErrMarklPurposeMissing    = errors.New("markl-id has no purpose (RFC 0005 §2 requires purpose@format-payload)")
	ErrMarklUnknownPurpose    = errors.New("markl-id purpose is not a content-digest purpose this build understands")
	ErrMarklFormatMismatch    = errors.New("markl-id format is not compatible with its purpose")
	ErrMarklUnsupportedFormat = errors.New("markl-id format cannot be verified by this build")
	ErrMarklDigestLength      = errors.New("markl-id digest has the wrong length for its format")
	ErrMarklMismatch          = errors.New("content does not match its markl-id pin")
)

// contentDigestPurposes maps each registered content-digest purpose this build
// accepts to the formats that purpose permits.
var contentDigestPurposes = map[string][]string{
	"dodder-blob-digest-sha256-v1": {formatSHA256, "blake2b256"},
}

// verifiableFormats maps each format this build can compute to its digest
// length. blake2b256 is registered for the purpose above but deliberately not
// computable here: it would add a golang.org/x/crypto dependency for a POC
// whose only pin can be sha256. A blake2b256 pin is therefore REJECTED before
// any fetch (RFC 0005 §2), never downgraded to unverified.
var verifiableFormats = map[string]int{
	formatSHA256: sha256.Size,
}

// PurposeContentDigestSHA256 is the purpose NewSHA256MarklID emits.
const PurposeContentDigestSHA256 = "dodder-blob-digest-sha256-v1"

const formatSHA256 = "sha256"

// NewSHA256MarklID returns the purpose-full sha256 markl-id of content.
func NewSHA256MarklID(content []byte) MarklID {
	sum := sha256.Sum256(content)

	return MarklID{Purpose: PurposeContentDigestSHA256, Format: formatSHA256, Digest: sum[:]}
}

// ParseMarklID parses a purpose-full markl-id and rejects any purpose or format
// this build cannot verify, so a pin it does not understand fails before its
// artifact is ever fetched.
func ParseMarklID(s string) (MarklID, error) {
	purpose, payload, ok := strings.Cut(s, "@")
	if !ok || purpose == "" {
		return MarklID{}, fmt.Errorf("%w: %q", ErrMarklPurposeMissing, s)
	}

	formats, ok := contentDigestPurposes[purpose]
	if !ok {
		return MarklID{}, fmt.Errorf("%w: %q", ErrMarklUnknownPurpose, purpose)
	}

	format, digest, err := blech32Decode(payload)
	if err != nil {
		return MarklID{}, fmt.Errorf("markl-id %q: %w", s, err)
	}

	if !slices.Contains(formats, format) {
		return MarklID{}, fmt.Errorf("%w: %q under %q", ErrMarklFormatMismatch, format, purpose)
	}

	size, ok := verifiableFormats[format]
	if !ok {
		return MarklID{}, fmt.Errorf("%w: %q (this POC verifies sha256 only)", ErrMarklUnsupportedFormat, format)
	}

	if len(digest) != size {
		return MarklID{}, fmt.Errorf("%w: %q has %d bytes, want %d", ErrMarklDigestLength, format, len(digest), size)
	}

	return MarklID{Purpose: purpose, Format: format, Digest: digest}, nil
}

// String renders the purpose-full spelling.
func (m MarklID) String() string {
	return m.Purpose + "@" + blech32Encode(m.Format, m.Digest)
}

// Verify reports whether content hashes to the pinned digest.
func (m MarklID) Verify(content []byte) error {
	var got []byte

	switch m.Format {
	case formatSHA256:
		sum := sha256.Sum256(content)
		got = sum[:]
	default:
		return fmt.Errorf("%w: %q", ErrMarklUnsupportedFormat, m.Format)
	}

	if subtle.ConstantTimeCompare(got, m.Digest) != 1 {
		return fmt.Errorf("%w: want %s, got %s", ErrMarklMismatch, m, MarklID{m.Purpose, m.Format, got})
	}

	return nil
}
