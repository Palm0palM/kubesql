package sql

import "strings"

func (p *parser) insertStatement() (*Statement, error) {
	p.advance() // INSERT
	if err := p.require(tokenInto, "INTO"); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.require(tokenIdentifier, "表名"); err != nil {
		return nil, err
	}
	stmt := &Statement{Type: "insert", Table: strings.ToLower(p.token.text)}
	p.advance()
	if err := p.require(tokenLeftParen, "("); err != nil {
		return nil, err
	}
	p.advance()
	columns, err := p.columnList()
	if err != nil {
		return nil, err
	}
	stmt.Columns = columns
	if err := p.require(tokenRightParen, ")"); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.require(tokenValues, "VALUES"); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.require(tokenLeftParen, "("); err != nil {
		return nil, err
	}
	p.advance()
	for {
		value, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		stmt.Values = append(stmt.Values, value)
		if p.token.kind != tokenComma {
			break
		}
		p.advance()
	}
	if err := p.require(tokenRightParen, ")"); err != nil {
		return nil, err
	}
	p.advance()
	return stmt, nil
}
