package compiler

import (
	"strings"
	"unicode/utf8"
)

func lex(filename string, source []byte) ([]token, error) {
	if !utf8.Valid(source) {
		return nil, diagnostic(filename, position{1, 1}, "source must be valid UTF-8")
	}
	runes := []rune(string(source))
	var tokens []token
	line, column := 1, 1
	for i := 0; i < len(runes); {
		ch := runes[i]
		pos := position{line, column}
		switch {
		case ch == ' ' || ch == '\t':
			i++
			column++
		case ch == '\n' || ch == '\r':
			if ch == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
				i++
			}
			tokens = append(tokens, token{"newline", "\n", pos})
			i++
			line++
			column = 1
		case ch == '/' && i+1 < len(runes) && runes[i+1] == '/':
			for i < len(runes) && runes[i] != '\n' && runes[i] != '\r' {
				i++
				column++
			}
		case isLetter(ch):
			start := i
			for i < len(runes) && (isLetter(runes[i]) || isDigit(runes[i])) {
				i++
				column++
			}
			tokens = append(tokens, token{"name", string(runes[start:i]), pos})
		case isDigit(ch):
			start := i
			for i < len(runes) && isDigit(runes[i]) {
				i++
				column++
			}
			tokens = append(tokens, token{"integer", string(runes[start:i]), pos})
		default:
			if i+1 < len(runes) {
				pair := string(runes[i : i+2])
				switch pair {
				case "==", "!=", "<=", ">=", "&&", "||", "+=", "-=", "*=":
					tokens = append(tokens, token{pair, pair, pos})
					i += 2
					column += 2
					continue
				}
			}
			if !strings.ContainsRune("{}():;=+-*<>!", ch) {
				return nil, diagnostic(filename, pos, "unexpected character %q", ch)
			}
			text := string(ch)
			tokens = append(tokens, token{text, text, pos})
			i++
			column++
		}
	}
	return append(tokens, token{"eof", "end of file", position{line, column}}), nil
}

func isLetter(ch rune) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '_'
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

func isKeyword(text string) bool {
	switch text {
	case "model", "state", "action", "require", "invariant", "Int", "Bool", "true", "false":
		return true
	}
	return false
}
