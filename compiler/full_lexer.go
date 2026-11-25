package compiler

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Module syntax keeps newlines as whitespace. Semicolons delimit declarations,
// so multiline expressions never depend on TLA+'s indentation rules.
func lexFull(filename string, source []byte) ([]token, error) {
	if !utf8.Valid(source) {
		return nil, diagnostic(filename, position{1, 1}, "source must be valid UTF-8")
	}
	runes := []rune(string(source))
	var result []token
	i, line, column := 0, 1, 1
	advance := func() {
		if runes[i] == '\r' {
			line++
			column = 1
		} else if runes[i] == '\n' {
			if i == 0 || runes[i-1] != '\r' {
				line++
			}
			column = 1
		} else {
			column++
		}
		i++
	}
	has := func(s string) bool {
		chars := []rune(s)
		if len(runes)-i < len(chars) {
			return false
		}
		for j, ch := range chars {
			if runes[i+j] != ch {
				return false
			}
		}
		return true
	}
	for i < len(runes) {
		pos := position{line, column}
		ch := runes[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			advance()
			continue
		}
		if has("//") || has(`\*`) {
			for i < len(runes) && runes[i] != '\n' && runes[i] != '\r' {
				advance()
			}
			continue
		}
		if has("/*") || has("(*") {
			open, close := "/*", "*/"
			if has("(*") {
				open, close = "(*", "*)"
			}
			advance()
			advance()
			depth := 1
			for i < len(runes) && depth > 0 {
				switch {
				case has(open):
					advance()
					advance()
					depth++
				case has(close):
					advance()
					advance()
					depth--
				default:
					advance()
				}
			}
			if depth != 0 {
				return result, diagnostic(filename, pos, "unterminated block comment")
			}
			continue
		}
		if has("WF_") || has("SF_") {
			value := string(runes[i : i+3])
			advance()
			advance()
			advance()
			result = append(result, token{"name", value, pos})
			continue
		}
		start := i
		if ch == '"' {
			advance()
			closed := false
			for i < len(runes) {
				if runes[i] == '\n' || runes[i] == '\r' {
					break
				}
				if runes[i] == '"' {
					advance()
					closed = true
					break
				}
				if runes[i] == '\\' {
					advance()
					if i == len(runes) || runes[i] == '\n' || runes[i] == '\r' {
						break
					}
				}
				advance()
			}
			if !closed {
				return result, diagnostic(filename, pos, "unterminated string literal")
			}
			result = append(result, token{"string", string(runes[start:i]), pos})
			continue
		}
		if ch == '`' {
			advance()
			for i < len(runes) && runes[i] != '`' && runes[i] != '\n' && runes[i] != '\r' {
				advance()
			}
			if i == len(runes) || runes[i] != '`' {
				return result, diagnostic(filename, pos, "unterminated quoted name or operator")
			}
			value := string(runes[start+1 : i])
			advance()
			kind := "nativeop"
			if validFullName(value) {
				kind = "quotedname"
			} else if !slices.Contains(fullOperators, value) {
				return result, diagnostic(filename, pos, "unknown TLA+ operator %q", value)
			}
			result = append(result, token{kind, value, pos})
			continue
		}
		if ch == '<' && i+2 < len(runes) && (isDigit(runes[i+1]) || runes[i+1] == '*' || runes[i+1] == '+') {
			end := i + 1
			for end < len(runes) && (isDigit(runes[end]) || runes[end] == '*' || runes[end] == '+') {
				end++
			}
			if end < len(runes) && runes[end] == '>' {
				end++
				for end < len(runes) && (isLetter(runes[end]) || isDigit(runes[end])) {
					end++
				}
				for i < end {
					advance()
				}
				result = append(result, token{"stepref", string(runes[start:i]), pos})
				continue
			}
		}
		if ch == '\\' && i+2 < len(runes) && strings.ContainsRune("bBoOhH", runes[i+1]) && (isDigit(runes[i+2]) || runes[i+1] == 'h' || runes[i+1] == 'H') {
			advance()
			advance()
			for i < len(runes) && (isDigit(runes[i]) || runes[i] >= 'a' && runes[i] <= 'f' || runes[i] >= 'A' && runes[i] <= 'F') {
				advance()
			}
			result = append(result, token{"number", string(runes[start:i]), pos})
			continue
		}
		if isLetter(ch) || isDigit(ch) {
			for i < len(runes) && (isLetter(runes[i]) || isDigit(runes[i])) {
				advance()
			}
			value := string(runes[start:i])
			kind := "name"
			if strings.Trim(value, "0123456789") == "" {
				kind = "number"
				if i+1 < len(runes) && runes[i] == '.' && isDigit(runes[i+1]) {
					advance()
					for i < len(runes) && isDigit(runes[i]) {
						advance()
					}
					value = string(runes[start:i])
				}
			}
			result = append(result, token{kind, value, pos})
			continue
		}
		if ch == '.' && i+1 < len(runes) && isDigit(runes[i+1]) {
			advance()
			for i < len(runes) && isDigit(runes[i]) {
				advance()
			}
			result = append(result, token{"number", string(runes[start:i]), pos})
			continue
		}
		matched := ""
		for _, op := range fullOperators {
			if len(op) > len(matched) && has(op) {
				matched = op
			}
		}
		if matched == "" {
			return result, diagnostic(filename, pos, "unexpected character %q", ch)
		}
		for range matched {
			advance()
		}
		result = append(result, token{matched, matched, pos})
	}
	return append(result, token{"eof", "end of file", position{line, column}}), nil
}

func validFullName(s string) bool {
	letter := false
	for _, ch := range s {
		if !isLetter(ch) && !isDigit(ch) {
			return false
		}
		letter = letter || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
	}
	// WF_ and SF_ are valid Names, but longer names with those prefixes are not.
	// Identifier positions separately reject these reserved fairness keywords.
	return letter && !(len(s) > 3 && (strings.HasPrefix(s, "WF_") || strings.HasPrefix(s, "SF_")))
}

// TLA+2's complete ASCII operator vocabulary, plus DSL delimiters/aliases.
var fullOperators = []string{
	"{", "}", "(", ")", "[", "]", ";", ",", ":", "_", "!", "@", "<<", ">>", "]_", ">>_", "|->", "->", "<-", "==", "!=", "+=", "-=", "*=", "!", "~", "[]", "<>",
	"!!", "#", "##", "$", "$$", "%", "%%", "&", "&&", "(+)", "(-)", "(.)", "(/)", `(\X)`, "*", "**", "+", "++", "-", "-.", "-+->", "--", "-|", "..", "...", "/", "//", "/=", `/\`, "::=", "::", ":=", ":>", "<", "<:", "<=>", "=", "=<", "=>", "=|", ">", ">=", "??", "@@", `\`, `\/`, "^", "^^", "|", "|-", "|=", "||", "~>", ".", "<=", "^+", "^*", "^#", "'",
	`\approx`, `\geq`, `\oslash`, `\sqsupseteq`, `\asymp`, `\gg`, `\otimes`, `\star`, `\bigcirc`, `\in`, `\prec`, `\subset`, `\bullet`, `\intersect`, `\preceq`, `\subseteq`, `\cap`, `\land`, `\propto`, `\succ`, `\cdot`, `\leq`, `\sim`, `\succeq`, `\circ`, `\ll`, `\simeq`, `\supset`, `\cong`, `\lor`, `\sqcap`, `\supseteq`, `\cup`, `\o`, `\sqcup`, `\union`, `\div`, `\odot`, `\sqsubset`, `\uplus`, `\doteq`, `\ominus`, `\sqsubseteq`, `\wr`, `\equiv`, `\oplus`, `\sqsupset`, `\notin`, `\lnot`, `\neg`, `\A`, `\E`, `\AA`, `\EE`, `\X`, `\times`,
}
