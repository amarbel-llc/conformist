// Copyright (c) 2017 Takatoshi Nakagawa
// Copyright (c) 2019 The age Authors
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package profile

// blech32 is bech32 (BIP173) with the HRP/data separator changed from `1` to
// `-`, the encoding of a markl-id's `format-payload` half (piggy RFC 0011).
//
// This is a port of piggy's go/internal/alfa/blech32 (itself derived from the
// age authors' bech32, hence the MIT notice above), reduced to what a profile
// needs and carrying piggy's RFC 0011 rulings: lowercase only, an HRP charset
// of [a-z0-9_], and a single-separator split. It is copied rather than imported
// because piggy's package is internal. Conformance against madder's own encoder
// is pinned by vectors in the tests, so a divergence fails there rather than as
// a pin that silently never verifies.

import (
	"errors"
	"fmt"
	"strings"
)

const blech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// blech32ChecksumLen is the checksum width in data characters.
const blech32ChecksumLen = 6

var blech32Generator = [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}

var (
	ErrBlech32Case             = errors.New("blech32: must be lowercase")
	ErrBlech32SeparatorMissing = errors.New("blech32: separator '-' missing")
	ErrBlech32EmptyHRP         = errors.New("blech32: empty HRP")
	ErrBlech32InvalidHRP       = errors.New("blech32: HRP must match [a-z0-9_]")
	ErrBlech32TooShort         = errors.New("blech32: data portion shorter than its checksum")
	ErrBlech32InvalidChar      = errors.New("blech32: invalid data character")
	ErrBlech32Checksum         = errors.New("blech32: invalid checksum")
	ErrBlech32Padding          = errors.New("blech32: invalid padding")
)

func blech32Polymod(values []byte) uint32 {
	chk := uint32(1)

	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)

		for i, g := range blech32Generator {
			if (top>>i)&1 == 1 {
				chk ^= g
			}
		}
	}

	return chk
}

func blech32HRPExpand(hrp string) []byte {
	ret := make([]byte, 0, len(hrp)*2+1)

	for i := range len(hrp) {
		ret = append(ret, hrp[i]>>5)
	}

	ret = append(ret, 0)

	for i := range len(hrp) {
		ret = append(ret, hrp[i]&31)
	}

	return ret
}

func blech32ConvertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	var (
		acc  uint32
		bits uint
		ret  []byte
	)

	maxv := uint32(1)<<toBits - 1

	for _, value := range data {
		acc = acc<<fromBits | uint32(value)
		bits += fromBits

		for bits >= toBits {
			bits -= toBits
			ret = append(ret, byte(acc>>bits&maxv)) //nolint:gosec // masked to toBits <= 8 bits
		}
	}

	switch {
	case pad:
		if bits > 0 {
			ret = append(ret, byte(acc<<(toBits-bits)&maxv)) //nolint:gosec // masked to toBits <= 8 bits
		}
	case bits >= fromBits, acc<<(toBits-bits)&maxv != 0:
		return nil, ErrBlech32Padding
	}

	return ret, nil
}

// blech32Encode encodes data under hrp. hrp must already satisfy the charset
// rules; it is only ever a markl format id this package itself validated.
func blech32Encode(hrp string, data []byte) string {
	values, _ := blech32ConvertBits(data, 8, 5, true) // padding cannot fail

	checksumInput := append(blech32HRPExpand(hrp), values...)
	checksumInput = append(checksumInput, make([]byte, blech32ChecksumLen)...)
	mod := blech32Polymod(checksumInput) ^ 1

	var out strings.Builder

	out.WriteString(hrp)
	out.WriteByte('-')

	for _, v := range values {
		out.WriteByte(blech32Charset[v])
	}

	for i := range blech32ChecksumLen {
		out.WriteByte(blech32Charset[mod>>(5*(5-i))&31])
	}

	return out.String()
}

// blech32Decode splits input at its single separator and returns the HRP and
// the decoded payload bytes, verifying the checksum.
func blech32Decode(input string) (string, []byte, error) {
	if strings.ToLower(input) != input {
		return "", nil, ErrBlech32Case
	}

	pos := strings.IndexByte(input, '-')

	switch {
	case pos < 0:
		return "", nil, ErrBlech32SeparatorMissing
	case pos == 0:
		return "", nil, ErrBlech32EmptyHRP
	case len(input)-(pos+1) <= blech32ChecksumLen:
		return "", nil, ErrBlech32TooShort
	}

	hrp := input[:pos]

	for _, c := range hrp {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return "", nil, fmt.Errorf("%w: %q", ErrBlech32InvalidHRP, hrp)
		}
	}

	dataPart := input[pos+1:]
	values := make([]byte, 0, len(dataPart))

	for i, c := range dataPart {
		d := strings.IndexRune(blech32Charset, c)
		if d < 0 {
			return "", nil, fmt.Errorf("%w %q at position %d", ErrBlech32InvalidChar, c, pos+1+i)
		}

		values = append(values, byte(d)) //nolint:gosec // d indexes the 32-character charset
	}

	if blech32Polymod(append(blech32HRPExpand(hrp), values...)) != 1 {
		return "", nil, ErrBlech32Checksum
	}

	payload, err := blech32ConvertBits(values[:len(values)-blech32ChecksumLen], 5, 8, false)
	if err != nil {
		return "", nil, err
	}

	return hrp, payload, nil
}
