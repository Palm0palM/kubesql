package sql

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func parseWhere(t *testing.T, text string) Expression {
	t.Helper()
	stmt, err := Parse("SELECT name FROM deployments WHERE " + text)
	if err != nil {
		t.Fatal(err)
	}
	return stmt.Where
}

func TestWherePrecedence(t *testing.T) {
	expr := parseWhere(t, "NOT name = 'web' OR name = 'worker' AND replicas >= 3")
	or := expr.(*BinaryExpression)
	if or.Operator != "OR" || or.Right.(*BinaryExpression).Operator != "AND" {
		t.Fatalf("incorrect OR/AND tree: %+v", expr)
	}
	not := or.Left.(*UnaryExpression)
	if not.Operator != "NOT" || not.Operand.(*BinaryExpression).Operator != "=" {
		t.Fatalf("NOT must bind after comparison: %+v", not)
	}
	expr = parseWhere(t, "(name = 'web' OR name = 'worker') AND replicas >= 3")
	and := expr.(*BinaryExpression)
	if and.Operator != "AND" || and.Left.(*BinaryExpression).Operator != "OR" {
		t.Fatalf("parentheses ignored: %+v", expr)
	}
	null := parseWhere(t, "NOT name IS NOT NULL").(*UnaryExpression)
	if null.Operator != "NOT" || null.Operand.(*UnaryExpression).Operator != "IS NOT NULL" {
		t.Fatal("IS NULL must bind before NOT")
	}
}

func TestWhereLiteralsAndASTJSON(t *testing.T) {
	for text, want := range map[string]string{
		"+0010": "10", "-.5": "-0.5", ".25": "0.25", "3.": "3",
		"9007199254740993": "9007199254740993", "-0.001": "-0.001",
	} {
		literal := parseWhere(t, text).(*Literal)
		if literal.Value != json.Number(want) {
			t.Fatalf("%q: value = %v, want %s", text, literal.Value, want)
		}
		if _, err := json.Marshal(literal); err != nil {
			t.Fatalf("%q: invalid AST JSON: %v", text, err)
		}
	}
	if got := parseWhere(t, "'it''s\nSQL'").(*Literal).Value; got != "it's\nSQL" {
		t.Fatalf("SQL string escape = %q", got)
	}
	for text, want := range map[string]any{"TRUE": true, "FALSE": false, "NULL": nil, "''": ""} {
		if got := parseWhere(t, text).(*Literal).Value; got != want {
			t.Fatalf("%q: value = %v, want %v", text, got, want)
		}
	}
	if got := parseWhere(t, "RePlicas").(*ColumnReference).Name; got != "replicas" {
		t.Fatalf("column reference = %q", got)
	}
}

func TestWhereOperators(t *testing.T) {
	for _, op := range []string{"=", "<>", ">", ">=", "<", "<="} {
		if got := parseWhere(t, "replicas"+op+"2").(*BinaryExpression).Operator; got != op {
			t.Fatalf("operator = %q, want %s", got, op)
		}
	}
}

func TestMalformedWhere(t *testing.T) {
	for _, text := range []string{
		"", "name =", "AND TRUE", "TRUE OR", "NOT", "()", "(TRUE", "TRUE)",
		"name IS TRUE", "name IS NOT", "1 < 2 < 3", "replicas != 2", "name = 'unterminated",
		"+", ".", "1.2.3", "1e2", "name = 'ok' 'sensitive-payload'",
	} {
		stmt, err := Parse("SELECT name FROM deployments WHERE " + text)
		var detail *ParseError
		if stmt != nil || !errors.As(err, &detail) || detail.Code != "E_PARSE" {
			t.Fatalf("%q: statement = %+v, error = %v", text, stmt, err)
		}
		if strings.Contains(detail.Message, "sensitive-payload") {
			t.Fatal("diagnostic leaked SQL literal contents")
		}
	}
}

func TestWhereStringPositions(t *testing.T) {
	stmt, err := Parse("SELECT name FROM deployments WHERE\nname = 'it''s' AND\nreplicas >= 2;")
	if err != nil {
		t.Fatal(err)
	}
	and := stmt.Where.(*BinaryExpression)
	if got := and.Right.(*BinaryExpression).Left.(*ColumnReference).Position; got.Line != 3 || got.Column != 1 {
		t.Fatalf("position after string = %+v", got)
	}
}
