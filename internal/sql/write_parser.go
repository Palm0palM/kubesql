package sql

import "strings"

func (p *parser) writeStatement() (*Statement, error) {
	stmt := &Statement{Type: "update"}
	if p.token.kind == tokenDelete {
		stmt.Type = "delete"
	}
	p.advance()
	if stmt.Type == "delete" {
		if err := p.require(tokenFrom, "FROM"); err != nil {
			return nil, err
		}
		p.advance()
	}
	if err := p.require(tokenIdentifier, "表名"); err != nil {
		return nil, err
	}
	stmt.Table = strings.ToLower(p.token.text)
	p.advance()
	if stmt.Type == "update" {
		if err := p.require(tokenSet, "SET"); err != nil {
			return nil, err
		}
		p.advance()
		for {
			if err := p.require(tokenIdentifier, "赋值列名"); err != nil {
				return nil, err
			}
			assignment := Assignment{Column: strings.ToLower(p.token.text), Position: p.token.start}
			p.advance()
			if err := p.require(tokenEqual, "="); err != nil {
				return nil, err
			}
			p.advance()
			value, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			assignment.Value = value
			stmt.Assignments = append(stmt.Assignments, assignment)
			if p.token.kind != tokenComma {
				break
			}
			p.advance()
		}
	}
	if err := p.optionalWhere(stmt); err != nil {
		return nil, err
	}
	// Missing WHERE is a semantic safety error, handled before any API access.
	return stmt, nil
}
