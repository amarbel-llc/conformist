package flakeparse

// LineStart returns the byte offset of the start of the line containing
// off (the index just after the preceding newline, or 0).
func LineStart(src []byte, off int) int {
	if off > len(src) {
		off = len(src)
	}
	i := off
	for i > 0 && src[i-1] != '\n' {
		i--
	}

	return i
}

// LineIndent returns the leading whitespace of the line containing byte
// offset off in src.
func LineIndent(src []byte, off int) string {
	if off > len(src) {
		off = len(src)
	}
	start := off
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	i := start
	for i < off && (src[i] == ' ' || src[i] == '\t') {
		i++
	}

	return string(src[start:i])
}

// OnlyBlankBefore reports whether everything between the start of off's
// line and off is whitespace — i.e. off (a closing brace) sits alone on
// its own line, so a line-start splice is safe.
func OnlyBlankBefore(src []byte, off int) bool {
	for i := LineStart(src, off); i < off; i++ {
		if src[i] != ' ' && src[i] != '\t' {
			return false
		}
	}

	return true
}

// afterSemicolon advances off past an immediately-following ';' (skipping
// intervening whitespace including newlines), so a flat-binding insert
// lands after the last binding's terminator.
func afterSemicolon(src []byte, off int) int {
	i := off
	for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n') {
		i++
	}
	if i < len(src) && src[i] == ';' {
		return i + 1
	}

	return off
}

// unquote strips surrounding double quotes from a quoted attr-name segment.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}

	return s
}
