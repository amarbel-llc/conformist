package profile

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
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
	ErrMarklUnknownPurpose    = errors.New("markl-id purpose is not " + PurposeArtifactDigest)
	ErrMarklUnsupportedFormat = errors.New("markl-id format is not a content digest this build can verify")
	ErrMarklDigestLength      = errors.New("markl-id digest has the wrong length for its format")
	ErrMarklMismatch          = errors.New("content does not match its markl-id pin")
)

// PurposeArtifactDigest is conformist's own purpose for an artifact pin: the
// content digest of an artifact a profile delivers (RFC 0005 §2). Purposes are
// owned by their domain, so conformist defines this one rather than borrowing a
// neighbour's (a dodder blob digest describes a dodder blob, not an artifact).
const PurposeArtifactDigest = "conformist-artifact-digest-v1"

// digestFormats maps every markl content-digest format (markl-id(7) FORMAT IDS)
// to the function computing it. The purpose accepts all of them; a format not
// listed cannot be verified and is REJECTED before any fetch (RFC 0005 §2).
var digestFormats = map[string]func([]byte) []byte{
	"sha256": func(b []byte) []byte {
		sum := sha256.Sum256(b)

		return sum[:]
	},
	"blake2b256": func(b []byte) []byte {
		sum := blake2b.Sum256(b)

		return sum[:]
	},
}

// NewMarklID returns the purpose-full artifact-digest markl-id of content in
// format, which must be a key of digestFormats.
func NewMarklID(format string, content []byte) MarklID {
	return MarklID{Purpose: PurposeArtifactDigest, Format: format, Digest: digestFormats[format](content)}
}

// ParseMarklID parses a purpose-full markl-id and rejects any purpose or format
// this build cannot verify, so a pin it does not understand fails before its
// artifact is ever fetched.
func ParseMarklID(s string) (MarklID, error) {
	purpose, payload, ok := strings.Cut(s, "@")
	if !ok || purpose == "" {
		return MarklID{}, fmt.Errorf("%w: %q", ErrMarklPurposeMissing, s)
	}

	if purpose != PurposeArtifactDigest {
		return MarklID{}, fmt.Errorf("%w: %q", ErrMarklUnknownPurpose, purpose)
	}

	format, digest, err := blech32Decode(payload)
	if err != nil {
		return MarklID{}, fmt.Errorf("markl-id %q: %w", s, err)
	}

	sum, ok := digestFormats[format]
	if !ok {
		return MarklID{}, fmt.Errorf("%w: %q", ErrMarklUnsupportedFormat, format)
	}

	if want := len(sum(nil)); len(digest) != want {
		return MarklID{}, fmt.Errorf("%w: %q has %d bytes, want %d", ErrMarklDigestLength, format, len(digest), want)
	}

	return MarklID{Purpose: purpose, Format: format, Digest: digest}, nil
}

// String renders the purpose-full spelling.
func (m MarklID) String() string {
	return m.Purpose + "@" + blech32Encode(m.Format, m.Digest)
}

// Verify reports whether content hashes to the pinned digest.
func (m MarklID) Verify(content []byte) error {
	sum, ok := digestFormats[m.Format]
	if !ok {
		return fmt.Errorf("%w: %q", ErrMarklUnsupportedFormat, m.Format)
	}

	got := sum(content)

	if subtle.ConstantTimeCompare(got, m.Digest) != 1 {
		return fmt.Errorf("%w: want %s, got %s", ErrMarklMismatch, m, MarklID{m.Purpose, m.Format, got})
	}

	return nil
}
