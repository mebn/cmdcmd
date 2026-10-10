package config

// stripJSONC turns JSON with comments (// and /* */) and trailing commas into
// plain JSON. Comment-like text inside strings is left alone.
func stripJSONC(src []byte) []byte {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '"':
			// Copy the whole string, honoring escapes.
			start := i
			for i++; i < len(src) && src[i] != '"'; i++ {
				if src[i] == '\\' {
					i++
				}
			}
			out = append(out, src[start:min(i+1, len(src))]...)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			for i += 2; i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/'); i++ {
			}
			i++ // skip the closing '/'
		case c == ',':
			// Drop a comma that only has whitespace or comments before } or ].
			if next := nextSignificant(src, i+1); next == '}' || next == ']' {
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// nextSignificant returns the next byte from i that is not whitespace or part
// of a comment, or 0 at the end.
func nextSignificant(src []byte, i int) byte {
	for i < len(src) {
		switch {
		case src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r':
			i++
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			for i += 2; i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/'); i++ {
			}
			i += 2
		default:
			return src[i]
		}
	}
	return 0
}
