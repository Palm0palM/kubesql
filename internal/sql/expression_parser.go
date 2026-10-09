package sql

import (
	"encoding/json"
	"strings"
)

func (p *parser) parseOr() (Expression, error) {
	return p.parseLogical(tokenOr, "OR", p.parseAnd)
}

func (p *parser) parseAnd() (Expression, error) {
	return p.parseLogical(tokenAnd, "AND", p.parseNot)
}

func (p *parser) parseLogical(kind tokenKind, operator string, operand func() (Expression, error)) (Expression, error) {
	left, err := operand()
	if err != nil {
		return nil, err
	}
	for p.err == nil && p.token.kind == kind {
		position := p.token.start
		p.advance()
		right, err := operand()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpression{Type: "binary", Operator: operator, Left: left, Right: right, Position: position}
	}
	return left, p.err
}

func (p *parser) parseNot() (Expression, error) {
	if p.err == nil && p.token.kind == tokenNot {
		position := p.token.start
		p.advance()
		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &UnaryExpression{Type: "unary", Operator: "NOT", Operand: operand, Position: position}, nil
	}
	return p.parsePredicate()
}

func (p *parser) parsePredicate() (Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	position := p.token.start
	if p.token.kind == tokenIs {
		p.advance()
		operator := "IS NULL"
		if p.token.kind == tokenNot {
			operator = "IS NOT NULL"
			p.advance()
		}
		if err := p.require(tokenNull, "NULL"); err != nil {
			return nil, err
		}
		p.advance()
		return &UnaryExpression{Type: "unary", Operator: operator, Operand: left, Position: position}, p.err
	}
	switch p.token.kind {
	case tokenEqual, tokenNotEqual, tokenGreater, tokenGreaterEqual, tokenLess, tokenLessEqual:
		operator := p.token.text
		p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &BinaryExpression{Type: "binary", Operator: operator, Left: left, Right: right, Position: position}, nil
	}
	return left, p.err
}

func (p *parser) parsePrimary() (Expression, error) {
	if p.err != nil {
		return nil, p.err
	}
	t := p.token
	if t.kind == tokenLeftParen {
		p.advance()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.require(tokenRightParen, "右括号"); err != nil {
			return nil, err
		}
		p.advance()
		return expr, p.err
	}
	var expr Expression
	switch t.kind {
	case tokenIdentifier:
		expr = &ColumnReference{Type: "column", Name: strings.ToLower(t.text), Position: t.start}
	case tokenString:
		expr = &Literal{Type: "literal", Value: t.text, Position: t.start}
	case tokenNumber:
		// Canonicalize SQL signs/decimal forms into a valid, exact JSON number.
		text := strings.TrimPrefix(t.text, "+")
		negative := strings.HasPrefix(text, "-")
		text = strings.TrimPrefix(text, "-")
		integer, fraction, decimal := strings.Cut(text, ".")
		integer = strings.TrimLeft(integer, "0")
		if integer == "" {
			integer = "0"
		}
		text = integer
		if decimal && fraction != "" {
			text += "." + fraction
		}
		if negative {
			text = "-" + text
		}
		expr = &Literal{Type: "literal", Value: json.Number(text), Position: t.start}
	case tokenTrue, tokenFalse:
		expr = &Literal{Type: "literal", Value: t.kind == tokenTrue, Position: t.start}
	case tokenNull:
		expr = &Literal{Type: "literal", Value: nil, Position: t.start}
	default:
		// require provides the existing structured position/message format.
		return nil, p.require(tokenIdentifier, "值或列引用")
	}
	p.advance()
	return expr, p.err
}
