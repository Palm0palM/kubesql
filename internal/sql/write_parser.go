package sql

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
	name, quoted, err := p.identifier("表名")
	if err != nil {
		return nil, err
	}
	stmt.Table, stmt.TableQuoted = name, quoted
	if stmt.Type == "update" {
		if err := p.require(tokenSet, "SET"); err != nil {
			return nil, err
		}
		p.advance()
		for {
			position := p.token.start
			name, quoted, err := p.identifier("赋值列名")
			if err != nil {
				return nil, err
			}
			assignment := Assignment{Column: name, Quoted: quoted, Position: position}
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
