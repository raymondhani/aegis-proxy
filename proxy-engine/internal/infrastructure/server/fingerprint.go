package server

import "strings"

// FingerprintSQL normalizes a SQL query into a stable fingerprint by replacing
// all literal values with '?' placeholders. This enables grouping semantically
// identical queries regardless of their parameter values.
//
// The normalizer performs a single O(n) pass over the input:
//  1. Replace single-quoted string literals ('...') with ?, handling escaped quotes ('it''s' → ?)
//  2. Replace standalone numeric literals with ? (digit sequences not preceded by an identifier char)
//  3. Collapse all contiguous whitespace into a single space
//  4. Lowercase everything
//  5. Trim trailing semicolons
//  6. Trim leading/trailing whitespace
func FingerprintSQL(query string) string {
	var b strings.Builder
	b.Grow(len(query))

	n := len(query)
	i := 0

	for i < n {
		ch := query[i]

		// 1. Handle single-quoted string literals
		if ch == '\'' {
			// Skip entire string literal, including escaped quotes ('')
			i++ // skip opening quote
			for i < n {
				if query[i] == '\'' {
					i++ // skip closing quote or first quote of escape
					if i < n && query[i] == '\'' {
						// Escaped quote (''), continue scanning
						i++
						continue
					}
					break
				}
				i++
			}
			b.WriteByte('?')
			continue
		}

		// 2. Handle numeric literals (standalone digits not preceded by identifier char)
		if isDigit(ch) {
			// Check if preceded by an identifier character (letter, digit, underscore)
			preceded := false
			if b.Len() > 0 {
				prev := b.String()[b.Len()-1]
				preceded = isIdentChar(prev)
			}

			if preceded {
				// Part of an identifier (e.g., table1), keep the digits
				for i < n && isDigit(query[i]) {
					b.WriteByte(lowerByte(query[i]))
					i++
				}
			} else {
				// Standalone numeric literal — skip all digits and optional decimal part
				for i < n && (isDigit(query[i]) || query[i] == '.') {
					i++
				}
				b.WriteByte('?')
			}
			continue
		}

		// 3. Collapse contiguous whitespace into a single space
		if isWhitespace(ch) {
			// Consume all contiguous whitespace
			for i < n && isWhitespace(query[i]) {
				i++
			}
			b.WriteByte(' ')
			continue
		}

		// 4. Lowercase and emit character as-is
		b.WriteByte(lowerByte(ch))
		i++
	}

	result := b.String()

	// 5. Trim trailing semicolons
	result = strings.TrimRight(result, ";")

	// 6. Trim leading/trailing whitespace
	result = strings.TrimSpace(result)

	return result
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}
