package sql

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type lexer struct {
	input string
	pos   Position
}

func newLexer(input string) *lexer {
	return &lexer{input: input, pos: Position{Line: 1, Column: 1}}
}

func (l *lexer) peek() (rune, int) {
	return utf8.DecodeRuneInString(l.input[l.pos.Offset:])
}

func (l *lexer) advance(r rune, size int) {
	l.pos.Offset += size
	if r == '\n' {
		l.pos.Line++
		l.pos.Column = 1
	} else {
		l.pos.Column++
	}
}

func (l *lexer) next() (token, error) {
	for l.pos.Offset < len(l.input) {
		r, size := l.peek()
		if !unicode.IsSpace(r) {
			break
		}
		l.advance(r, size)
	}

	start := l.pos
	if start.Offset == len(l.input) {
		return token{kind: tokenEOF, start: start, end: start}, nil
	}
	r, size := l.peek()
	kind := tokenIdentifier
	if r == '_' || unicode.IsLetter(r) {
		for l.pos.Offset < len(l.input) {
			r, size = l.peek()
			if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				break
			}
			l.advance(r, size)
		}
		switch strings.ToUpper(l.input[start.Offset:l.pos.Offset]) {
		case "SELECT":
			kind = tokenSelect
		case "FROM":
			kind = tokenFrom
		}
	} else {
		switch r {
		case '*':
			kind = tokenStar
		case ',':
			kind = tokenComma
		case ';':
			kind = tokenSemicolon
		default:
			message := fmt.Sprintf("unexpected character %q", r)
			if r == utf8.RuneError && size == 1 {
				message = "invalid UTF-8"
			}
			return token{}, &ParseError{Code: "E_PARSE", Position: start, Message: message}
		}
		l.advance(r, size)
	}
	return token{kind: kind, text: l.input[start.Offset:l.pos.Offset], start: start, end: l.pos}, nil
}
