package sql

import "testing"

func TestWriteSyntax(t *testing.T) {
	stmt, err := Parse(`UPDATE deployments SET replicas = 3, annotations = '{"owner":"alice"}' WHERE name = 'web';`)
	if err != nil || stmt.Type != "update" || stmt.Table != "deployments" || len(stmt.Assignments) != 2 || stmt.Where == nil {
		t.Fatalf("statement = %+v, error = %v", stmt, err)
	}
	if got := stmt.Assignments[1].Value.(*Literal).Value; got != `{"owner":"alice"}` {
		t.Fatalf("JSON SQL string = %v", got)
	}
	stmt, err = Parse(`delete FROM ingresses WHERE NOT name = 'web' OR name IS NULL`)
	if err != nil || stmt.Type != "delete" || stmt.Table != "ingresses" || stmt.Where == nil {
		t.Fatalf("statement = %+v, error = %v", stmt, err)
	}
	for _, input := range []string{`UPDATE deployments SET replicas = 3`, `DELETE FROM deployments`} {
		stmt, err := Parse(input)
		if err != nil || stmt.Where != nil {
			t.Fatalf("missing WHERE must reach semantic validation: %+v, %v", stmt, err)
		}
	}
}

func TestMalformedWriteSyntax(t *testing.T) {
	for _, input := range []string{
		"UPDATE SET replicas = 2 WHERE TRUE", "UPDATE deployments replicas = 2 WHERE TRUE",
		"UPDATE deployments SET WHERE TRUE", "UPDATE deployments SET replicas 2 WHERE TRUE",
		"UPDATE deployments SET replicas = WHERE TRUE", "UPDATE deployments SET replicas = 2, WHERE TRUE",
		"DELETE deployments WHERE TRUE", "DELETE FROM WHERE TRUE", "DELETE FROM deployments WHERE",
		"UPDATE deployments SET replicas = 2; DELETE FROM deployments WHERE TRUE",
	} {
		if stmt, err := Parse(input); err == nil || stmt != nil {
			t.Fatalf("invalid SQL accepted: %s", input)
		}
	}
}
