package profile

// Profile signatures (RFC 0005 §3.2), in the scheme papi defines for signing
// any hyphence document body included (papi RFC-0001 §15): a `-` metadata line
// carrying `conformist-profile-sig-v1@ecdsa_p256_sig-<blech32>`, a raw 64-byte
// r‖s ECDSA P-256 signature over SHA-256 of the signed input, made with the
// operator's slot-9A key (`papi hyphence sign --purpose conformist-profile-sig-v1`).
//
// The signed input is computed with the hyphence library itself, the same one
// papi's signer uses, so the canonical form cannot drift between producer and
// verifier.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"code.linenisgreat.com/hyphence/go/hyphence"
)

// SignaturePurpose is conformist's markl purpose for a profile signature.
// Purposes are owned by their domain, so conformist defines this one.
const SignaturePurpose = "conformist-profile-sig-v1"

const (
	purposePIVAuth     = "piggy-piv_auth-v1"
	formatSSHEcdsaP256 = "ssh_ecdsa_nistp256_pub"
	formatEcdsaP256Sig = "ecdsa_p256_sig"

	ecdsaP256SigLen = 64
	p256CoordLen    = 32
)

var (
	ErrUnsigned           = errors.New("profile carries no " + SignaturePurpose + " signature line")
	ErrMultipleSignatures = errors.New("profile carries more than one " + SignaturePurpose + " signature line")
	ErrSignatureMalformed = errors.New(
		"signature line is not a well-formed " + SignaturePurpose + "@" + formatEcdsaP256Sig + " markl-id",
	)
	ErrSignatureInvalid = errors.New("profile signature does not verify under any trusted key")
	ErrNoTrustedKey     = errors.New("no trusted signing key: pin one with --profile-key")
	ErrKeyNotPublished  = errors.New("no pinned signing key is published on the serving domain's /papi/piggy-ids")
	ErrMalformedKey     = errors.New(
		"signing key is not a well-formed " + purposePIVAuth + "@" + formatSSHEcdsaP256 + " markl-id",
	)
)

func isSignatureLine(l hyphence.MetadataLine) bool {
	return l.Prefix == '-' && strings.HasPrefix(l.Value, SignaturePurpose+"@")
}

// hasClosingBoundary reports whether data's metadata section, opened by its
// first `---` line, is closed by a second one.
func hasClosingBoundary(data []byte) bool {
	rest, ok := bytes.CutPrefix(data, []byte(hyphence.Boundary+"\n"))
	if !ok {
		return false
	}

	for line := range bytes.SplitSeq(rest, []byte("\n")) {
		if string(line) == hyphence.Boundary {
			return true
		}
	}

	return false
}

// readHyphence splits a hyphence document into its metadata lines and body.
func readHyphence(data []byte) ([]hyphence.MetadataLine, []byte, error) {
	// hyphence v0.4.0's Reader returns no error when the input ends before the
	// closing boundary, leaving its metadata goroutine unjoined, so a truncated
	// profile would be half-read. Refuse it here instead; drop this once
	// hyphence#15 is fixed.
	if !hasClosingBoundary(data) {
		return nil, nil, fmt.Errorf("%w: metadata section has no closing %q boundary", ErrNotHyphence, hyphence.Boundary)
	}

	doc := &hyphence.Document{}

	var body bytes.Buffer

	reader := hyphence.Reader{
		RequireMetadata: true,
		Metadata:        &hyphence.MetadataBuilder{Doc: doc},
		Blob:            &body,
	}

	if _, err := reader.ReadFrom(bytes.NewReader(data)); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNotHyphence, err)
	}

	return doc.Metadata, body.Bytes(), nil
}

// SignedInput returns the bytes a profile signature covers (papi RFC-0001
// §15.1): the metadata with every signature line removed, re-emitted in
// hyphence canonical form, then the blank separator and the body verbatim when
// there is a body. `%` comments trailing the last metadata line are not
// covered.
func SignedInput(lines []hyphence.MetadataLine, body []byte) ([]byte, error) {
	kept := make([]hyphence.MetadataLine, 0, len(lines))

	for _, l := range lines {
		if !isSignatureLine(l) {
			kept = append(kept, l)
		}
	}

	var buf bytes.Buffer

	emitter := &hyphence.FormatBodyEmitter{Doc: &hyphence.Document{Metadata: kept}, Out: &buf}
	if _, err := emitter.ReadFrom(bytes.NewReader(body)); err != nil {
		return nil, fmt.Errorf("re-encoding the signed input: %w", err)
	}

	return buf.Bytes(), nil
}

// ParseSigningKey parses a slot-9A public key markl-id
// (`piggy-piv_auth-v1@ssh_ecdsa_nistp256_pub-…`, a compressed P-256 point).
func ParseSigningKey(id string) (*ecdsa.PublicKey, error) {
	purpose, format, point, err := splitMarklID(id)
	if err != nil || purpose != purposePIVAuth || format != formatSSHEcdsaP256 {
		return nil, fmt.Errorf("%w: %q", ErrMalformedKey, id)
	}

	x, y := elliptic.UnmarshalCompressed(elliptic.P256(), point)
	if x == nil {
		return nil, fmt.Errorf("%w: %q is not a compressed P-256 point", ErrMalformedKey, id)
	}

	uncompressed := make([]byte, 1+2*p256CoordLen)
	uncompressed[0] = 0x04
	x.FillBytes(uncompressed[1 : 1+p256CoordLen])
	y.FillBytes(uncompressed[1+p256CoordLen:])

	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), uncompressed)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrMalformedKey, id, err)
	}

	return key, nil
}

// VerifySignature checks data's profile signature against trusted, the
// slot-9A key markl-ids the consumer accepts, and returns the id of the key
// that verified. Exactly one signature line must be present and it must
// verify under a trusted key: an unsigned profile is an error, never a pass
// (RFC 0005 §3.2).
func VerifySignature(data []byte, trusted []string) (string, error) {
	if len(trusted) == 0 {
		return "", ErrNoTrustedKey
	}

	keys := make([]*ecdsa.PublicKey, len(trusted))

	for i, id := range trusted {
		key, err := ParseSigningKey(id)
		if err != nil {
			return "", err
		}

		keys[i] = key
	}

	lines, body, err := readHyphence(data)
	if err != nil {
		return "", err
	}

	var sigValue string

	for _, l := range lines {
		if !isSignatureLine(l) {
			continue
		}

		if sigValue != "" {
			return "", ErrMultipleSignatures
		}

		sigValue = l.Value
	}

	if sigValue == "" {
		return "", ErrUnsigned
	}

	purpose, format, sig, err := splitMarklID(sigValue)
	if err != nil || purpose != SignaturePurpose || format != formatEcdsaP256Sig || len(sig) != ecdsaP256SigLen {
		return "", ErrSignatureMalformed
	}

	input, err := SignedInput(lines, body)
	if err != nil {
		return "", err
	}

	digest := sha256.Sum256(input)
	r := new(big.Int).SetBytes(sig[:p256CoordLen])
	s := new(big.Int).SetBytes(sig[p256CoordLen:])

	for i, key := range keys {
		if ecdsa.Verify(key, digest[:], r, s) {
			return trusted[i], nil
		}
	}

	return "", ErrSignatureInvalid
}

// PublishedSigningKeys extracts the slot-9A key ids from a /papi/piggy-ids body:
// one id per line, `#` comment lines skipped, and a trailing `# label` dropped.
func PublishedSigningKeys(piggyIDs []byte) []string {
	var ids []string

	for line := range strings.SplitSeq(string(piggyIDs), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}

		if strings.HasPrefix(fields[0], purposePIVAuth+"@") {
			ids = append(ids, fields[0])
		}
	}

	return ids
}
