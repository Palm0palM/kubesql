package sql

import "fmt"

// Position identifies a character using one-based line/column and a byte offset.
type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
	Offset int `json:"-"`
}

// ParseError describes invalid SQL, including lexical errors.
type ParseError struct {
	Code string `json:"code"`
	Position
	Message string `json:"message"`
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s at %d:%d: %s", e.Code, e.Line, e.Column, e.Message)
}

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenIdentifier
	tokenSelect
	tokenFrom
	tokenStar
	tokenComma
	tokenSemicolon
	tokenWhere
	tokenAnd
	tokenOr
	tokenNot
	tokenIs
	tokenNull
	tokenTrue
	tokenFalse
	tokenString
	tokenNumber
	tokenLeftParen
	tokenRightParen
	tokenEqual
	tokenNotEqual
	tokenGreater
	tokenGreaterEqual
	tokenLess
	tokenLessEqual
	tokenUpdate
	tokenSet
	tokenDelete
	tokenInsert
	tokenInto
	tokenValues
)

type token struct {
	kind  tokenKind
	text  string
	start Position
	end   Position // Exclusive: immediately after the last character.
}
