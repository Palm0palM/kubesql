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

var keywords = map[string]tokenKind{
	"SELECT": tokenSelect, "FROM": tokenFrom, "WHERE": tokenWhere,
	"AND": tokenAnd, "OR": tokenOr, "NOT": tokenNot, "IS": tokenIs,
	"NULL": tokenNull, "TRUE": tokenTrue, "FALSE": tokenFalse,
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
	if r == '\'' {
		return l.stringToken(start)
	}
	if isDigit(r) || r == '+' || r == '-' || r == '.' {
		return l.numberToken(start)
	}
	kind := tokenIdentifier
	if r == '_' || unicode.IsLetter(r) {
		for l.pos.Offset < len(l.input) {
			r, size = l.peek()
			if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				break
			}
			l.advance(r, size)
		}
		if keyword, ok := keywords[strings.ToUpper(l.input[start.Offset:l.pos.Offset])]; ok {
			kind = keyword
		}
	} else {
		switch r {
		case '*':
			kind = tokenStar
		case ',':
			kind = tokenComma
		case ';':
			kind = tokenSemicolon
		case '(':
			kind = tokenLeftParen
		case ')':
			kind = tokenRightParen
		case '=':
			kind = tokenEqual
		case '<', '>':
			kind = tokenLess
			if r == '>' {
				kind = tokenGreater
			}
			l.advance(r, size)
			if l.pos.Offset < len(l.input) {
				next, nextSize := l.peek()
				if next == '=' || (r == '<' && next == '>') {
					kind = tokenLessEqual
					if r == '>' {
						kind = tokenGreaterEqual
					} else if next == '>' {
						kind = tokenNotEqual
					}
					l.advance(next, nextSize)
				}
			}
			return token{kind: kind, text: l.input[start.Offset:l.pos.Offset], start: start, end: l.pos}, nil
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

func (l *lexer) stringToken(start Position) (token, error) {
	l.advance('\'', 1)
	var value strings.Builder
	for l.pos.Offset < len(l.input) {
		r, size := l.peek()
		if r == utf8.RuneError && size == 1 {
			return token{}, &ParseError{Code: "E_PARSE", Position: l.pos, Message: "invalid UTF-8"}
		}
		l.advance(r, size)
		if r == '\'' {
			if l.pos.Offset < len(l.input) && l.input[l.pos.Offset] == '\'' {
				l.advance('\'', 1)
				value.WriteRune('\'')
				continue
			}
			return token{kind: tokenString, text: value.String(), start: start, end: l.pos}, nil
		}
		value.WriteRune(r)
	}
	return token{}, &ParseError{Code: "E_PARSE", Position: start, Message: "unterminated SQL string"}
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func (l *lexer) numberToken(start Position) (token, error) {
	r, size := l.peek()
	if r == '+' || r == '-' {
		l.advance(r, size)
	}
	digits := 0
	decimal := false
	for l.pos.Offset < len(l.input) {
		r, size = l.peek()
		if isDigit(r) {
			digits++
		} else if r == '.' && !decimal {
			decimal = true
		} else {
			break
		}
		l.advance(r, size)
	}
	if digits == 0 {
		return token{}, &ParseError{Code: "E_PARSE", Position: start, Message: "expected a number"}
	}
	return token{kind: tokenNumber, text: l.input[start.Offset:l.pos.Offset], start: start, end: l.pos}, nil
}
