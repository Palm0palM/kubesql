package sql

import (
	"errors"
	"testing"
)

func TestLexerTokens(t *testing.T) {
	l := newLexer("select name, replicas FROM deployments;*")
	want := []struct {
		kind tokenKind
		text string
	}{
		{tokenSelect, "select"}, {tokenIdentifier, "name"}, {tokenComma, ","},
		{tokenIdentifier, "replicas"}, {tokenFrom, "FROM"},
		{tokenIdentifier, "deployments"}, {tokenSemicolon, ";"}, {tokenStar, "*"},
		{tokenEOF, ""},
	}
	for _, w := range want {
		got, err := l.next()
		if err != nil {
			t.Fatal(err)
		}
		if got.kind != w.kind || got.text != w.text {
			t.Fatalf("token = %+v, want kind %d text %q", got, w.kind, w.text)
		}
	}
}

func TestLexerPositions(t *testing.T) {
	l := newLexer(" \n名,\r\nFROM")
	want := []struct{ start, end Position }{
		{Position{2, 1, 2}, Position{2, 2, 5}},
		{Position{2, 2, 5}, Position{2, 3, 6}},
		{Position{3, 1, 8}, Position{3, 5, 12}},
		{Position{3, 5, 12}, Position{3, 5, 12}},
	}
	for _, w := range want {
		got, err := l.next()
		if err != nil {
			t.Fatal(err)
		}
		if got.start != w.start || got.end != w.end {
			t.Fatalf("span = %v..%v, want %v..%v", got.start, got.end, w.start, w.end)
		}
	}
}

func TestLexerIdentifierBoundaries(t *testing.T) {
	l := newLexer("SELECT_name from2 _replicas")
	for range 3 {
		got, err := l.next()
		if err != nil || got.kind != tokenIdentifier {
			t.Fatalf("token = %+v, error = %v", got, err)
		}
	}
}

func TestLexerErrorsAndEmptyInput(t *testing.T) {
	for _, input := range []string{"@", "'", "\xff"} {
		_, err := newLexer(input).next()
		var parseErr *ParseError
		if !errors.As(err, &parseErr) || parseErr.Code != "E_PARSE" || parseErr.Position != (Position{1, 1, 0}) {
			t.Fatalf("input %q: error = %v", input, err)
		}
	}
	l := newLexer("")
	for range 2 {
		got, err := l.next()
		if err != nil || got.kind != tokenEOF || got.start != (Position{1, 1, 0}) {
			t.Fatalf("empty input: token = %+v, error = %v", got, err)
		}
	}
}
