package sql

import (
	"fmt"
	"strings"
)

type parser struct {
	lexer *lexer
	token token
	err   error
}

// Parse accepts one SELECT, UPDATE, DELETE or INSERT, then optional semicolon/EOF.
// It performs no Kubernetes requests or table/column validation.
func Parse(input string) (*Statement, error) {
	p := &parser{lexer: newLexer(input)}
	p.advance()
	var stmt *Statement
	var err error
	switch p.token.kind {
	case tokenInsert:
		stmt, err = p.insertStatement()
	case tokenUpdate, tokenDelete:
		stmt, err = p.writeStatement()
	default:
		stmt, err = p.selectStatement()
	}
	if err != nil {
		return nil, err
	}
	if p.token.kind == tokenSemicolon {
		p.advance()
	}
	if err := p.require(tokenEOF, "语句结束"); err != nil {
		return nil, err
	}
	return stmt, nil
}

func (p *parser) advance() {
	if p.err == nil {
		p.token, p.err = p.lexer.next()
	}
}

func (p *parser) require(kind tokenKind, expected string) error {
	if p.err != nil {
		return p.err
	}
	if p.token.kind != kind {
		found := p.token.text
		if p.token.kind == tokenEOF {
			found = "EOF"
		} else if p.token.kind == tokenString {
			found = "字符串"
		} else if p.token.kind == tokenNumber {
			found = "数字"
		}
		return &ParseError{
			Code: "E_PARSE", Position: p.token.start,
			Message: fmt.Sprintf("这里需要%s，却遇到了 %s", expected, found),
		}
	}
	return nil
}

func (p *parser) selectStatement() (*Statement, error) {
	if err := p.require(tokenSelect, "SELECT"); err != nil {
		return nil, err
	}
	p.advance()
	stmt := &Statement{Type: "select"}
	if p.token.kind == tokenStar && p.err == nil {
		stmt.Columns = []Column{{Type: "star", Position: p.token.start}}
		p.advance()
	} else {
		columns, err := p.columnList(true)
		if err != nil {
			return nil, err
		}
		stmt.Columns = columns
	}
	if err := p.require(tokenFrom, "FROM"); err != nil {
		return nil, err
	}
	p.advance()
	name, quoted, err := p.identifier("表名")
	if err != nil {
		return nil, err
	}
	stmt.Table, stmt.TableQuoted = name, quoted
	if err := p.optionalWhere(stmt); err != nil {
		return nil, err
	}
	return stmt, nil
}

func (p *parser) optionalWhere(stmt *Statement) error {
	if p.err == nil && p.token.kind == tokenWhere {
		p.advance()
		where, err := p.parseOr()
		if err != nil {
			return err
		}
		stmt.Where = where
	}
	return p.err
}

func (p *parser) columnList(allowAlias bool) ([]Column, error) {
	var columns []Column
	for {
		position := p.token.start
		name, quoted, err := p.identifier("列名")
		if err != nil {
			return nil, err
		}
		c := Column{Type: "column", Name: name, Quoted: quoted, Position: position}
		if allowAlias && p.token.kind == tokenAs {
			p.advance()
			position := p.token.start
			alias, _, err := p.identifier("别名")
			if err != nil {
				return nil, err
			}
			if alias == "" {
				return nil, &ParseError{Code: "E_PARSE", Position: position, Message: "empty alias is unsupported"}
			}
			c.Alias = alias
		}
		columns = append(columns, c)
		if p.err != nil {
			return nil, p.err
		}
		if p.token.kind != tokenComma {
			return columns, nil
		}
		p.advance()
	}
}

func (p *parser) identifier(expected string) (string, bool, error) {
	if p.err != nil {
		return "", false, p.err
	}
	quoted := p.token.kind == tokenQuotedIdentifier
	if !quoted {
		if err := p.require(tokenIdentifier, expected); err != nil {
			return "", false, err
		}
	}
	text := p.token.text
	if !quoted {
		text = strings.ToLower(text)
	}
	p.advance()
	return text, quoted, p.err
}
