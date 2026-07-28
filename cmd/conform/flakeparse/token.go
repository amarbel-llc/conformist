package flakeparse

// TokenIndex returns the byte offset of needle within s when needle
// appears as a complete Nix identifier or dotted-attr-path token (not
// embedded inside a longer identifier). Returns -1 when not found.
func TokenIndex(s, needle string) int {
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

		return i
	}

	return -1
}

// IsNixIdentChar reports whether r can appear inside a Nix identifier
// or dotted attr-path (letters, digits, _, -, ', .).
func IsNixIdentChar(r rune) bool {
	return r == '_' || r == '-' || r == '\'' || r == '.' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}
