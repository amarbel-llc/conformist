package flakeparse

// TokenIndex returns the byte offset of the FIRST occurrence of needle
// within s when needle appears as a complete Nix identifier or
// dotted-attr-path token (not embedded inside a longer identifier).
// Returns -1 when not found. Callers that must not half-apply an edit
// should use TokenIndices and reject a multi-occurrence result.
func TokenIndex(s, needle string) int {
	if idx := TokenIndices(s, needle); len(idx) > 0 {
		return idx[0]
	}

	return -1
}

// TokenIndices returns the byte offsets of EVERY complete-token occurrence
// of needle within s, in ascending order. Nil when there are none.
func TokenIndices(s, needle string) []int {
	if needle == "" {
		return nil
	}

	var out []int

	for i := 0; i <= len(s)-len(needle); i++ {
		if s[i:i+len(needle)] != needle {
			continue
		}
		if i > 0 && IsNixIdentChar(rune(s[i-1])) {
			continue
		}
		end := i + len(needle)
		if end < len(s) && IsNixIdentChar(rune(s[end])) {
			continue
		}

		out = append(out, i)
	}

	return out
}

// IsNixIdentChar reports whether r can appear inside a Nix identifier
// or dotted attr-path (letters, digits, _, -, ', .).
func IsNixIdentChar(r rune) bool {
	return r == '_' || r == '-' || r == '\'' || r == '.' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}
