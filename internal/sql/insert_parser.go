package sql

func (p *parser) insertStatement() (*Statement, error) {
	p.advance() // INSERT
	if err := p.require(tokenInto, "INTO"); err != nil {
		return nil, err
	}
	p.advance()
	name, quoted, err := p.identifier("表名")
	if err != nil {
		return nil, err
	}
	stmt := &Statement{Type: "insert", Table: name, TableQuoted: quoted}
	if err := p.require(tokenLeftParen, "("); err != nil {
		return nil, err
	}
	p.advance()
	columns, err := p.columnList(false)
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
