package configcodec

// preflight caps bracket recursion and dotted-key chains before entering the
// TOML decoder. checkDepth/sanitize enforce the combined semantic limit after
// decoding. This is only a resource guard; the TOML parser still validates syntax.
func preflight(data []byte) error {
	brackets, dots := 0, 0
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c == '#' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			dots = 0
			continue
		}
		if c == '"' || c == '\'' {
			quote := c
			multi := i+2 < len(data) && data[i+1] == quote && data[i+2] == quote
			if multi {
				i += 2
			}
			for i++; i < len(data); i++ {
				if quote == '"' && data[i] == '\\' {
					i++
					continue
				}
				if data[i] != quote {
					continue
				}
				if !multi {
					break
				}
				if i+2 < len(data) && data[i+1] == quote && data[i+2] == quote {
					i += 2
					// TOML permits one or two quotes immediately before the closing three.
					for n := 0; n < 2 && i+1 < len(data) && data[i+1] == quote; n++ {
						i++
					}
					break
				}
			}
			continue
		}
		switch c {
		case '[', '{':
			brackets++
			dots = 0
			if brackets > maxDepth {
				return ErrDepth
			}
		case ']', '}':
			brackets--
			if brackets < 0 {
				return ErrSyntax
			}
			dots = 0
		case '.':
			dots++
			if dots >= maxDepth {
				return ErrDepth
			}
		case '=', ',', '\n', '\r':
			dots = 0
		}
	}
	return nil
}
