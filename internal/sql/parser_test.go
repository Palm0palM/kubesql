package sql

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestParseSelectAST(t *testing.T) {
	tests := []struct{ input, want string }{
		{"select name, replicas FROM deployments;", `{"type":"select","columns":[{"type":"column","name":"name"},{"type":"column","name":"replicas"}],"table":"deployments"}`},
		{"SeLeCt\n * fRoM INGRESSES", `{"type":"select","columns":[{"type":"star"}],"table":"ingresses"}`},
		{"SELECT Unknown_Column FROM unknown_table; \n", `{"type":"select","columns":[{"type":"column","name":"unknown_column"}],"table":"unknown_table"}`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			stmt, err := Parse(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(stmt)
			if err != nil {
				t.Fatal(err)
			}
			var gotJSON, wantJSON any
			if err := json.Unmarshal(got, &gotJSON); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.want), &wantJSON); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotJSON, wantJSON) {
				t.Fatalf("AST = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestParseErrorJSON(t *testing.T) {
	stmt, err := Parse("SELECT name, FROM deployments;")
	var parseErr *ParseError
	if stmt != nil || !errors.As(err, &parseErr) {
		t.Fatalf("statement = %+v, error = %v", stmt, err)
	}
	got, err := json.Marshal(parseErr)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"code":"E_PARSE","line":1,"column":14,"message":"这里需要列名，却遇到了 FROM"}`
	if string(got) != want {
		t.Fatalf("error = %s, want %s", got, want)
	}
}

func TestParseRejectsInvalidStatements(t *testing.T) {
	tests := []struct {
		input        string
		line, column int
	}{
		{"", 1, 1}, {" \n", 2, 1}, {"name FROM deployments", 1, 1},
		{"SELECT FROM deployments", 1, 8}, {"SELECT name deployments", 1, 13},
		{"SELECT name FROM", 1, 17}, {"SELECT name,\nFROM deployments", 2, 1},
		{"SELECT *, name FROM deployments", 1, 9}, {"SELECT name, * FROM deployments", 1, 14},
		{"SELECT name FROM deployments extra", 1, 30},
		{"SELECT * FROM deployments;;", 1, 27},
		{"SELECT * FROM deployments; SELECT * FROM namespaces", 1, 28},
		{"SELECT @ FROM deployments", 1, 8}, {"SELECT name FROM deployments; @", 1, 31},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			stmt, err := Parse(tt.input)
			var parseErr *ParseError
			if stmt != nil || !errors.As(err, &parseErr) {
				t.Fatalf("statement = %+v, error = %v", stmt, err)
			}
			if parseErr.Code != "E_PARSE" || parseErr.Line != tt.line || parseErr.Column != tt.column {
				t.Fatalf("error = %v, want E_PARSE at %d:%d", err, tt.line, tt.column)
			}
		})
	}
}

func TestParsePreservesColumnPositions(t *testing.T) {
	stmt, err := Parse("SELECT name,\nreplicas FROM deployments")
	if err != nil {
		t.Fatal(err)
	}
	if got := stmt.Columns[1].Position; got != (Position{2, 1, 13}) {
		t.Fatalf("column position = %+v", got)
	}
}
