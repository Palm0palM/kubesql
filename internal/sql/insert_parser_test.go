package sql

import "testing"

func TestInsertSyntaxAndEscaping(t *testing.T) {
	stmt, err := Parse(`insert INTO namespaces (MANIFEST) VALUES ('{"apiVersion":"v1","metadata":{"name":"alice''s","annotations":{"text":"a\\b"}}}');`)
	if err != nil || stmt.Type != "insert" || stmt.Table != "namespaces" || len(stmt.Columns) != 1 || stmt.Columns[0].Name != "manifest" || len(stmt.Values) != 1 {
		t.Fatalf("statement = %+v, error = %v", stmt, err)
	}
	if got := stmt.Values[0].(*Literal).Value; got != `{"apiVersion":"v1","metadata":{"name":"alice's","annotations":{"text":"a\\b"}}}` {
		t.Fatalf("SQL string decoded incorrectly: %v", got)
	}
	// SQL parser does not validate JSON or table-specific columns.
	if _, err := Parse(`INSERT INTO deployments (name, manifest) VALUES ('web', 'not json')`); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedInsertSyntax(t *testing.T) {
	for _, input := range []string{
		"INSERT namespaces (manifest) VALUES ('{}')", "INSERT INTO (manifest) VALUES ('{}')",
		"INSERT INTO namespaces manifest VALUES ('{}')", "INSERT INTO namespaces () VALUES ('{}')",
		"INSERT INTO namespaces (manifest,) VALUES ('{}')", "INSERT INTO namespaces (manifest) ('{}')",
		"INSERT INTO namespaces (manifest) VALUES '{}'", "INSERT INTO namespaces (manifest) VALUES ()",
		"INSERT INTO namespaces (manifest) VALUES ('{}',)", "INSERT INTO namespaces (manifest) VALUES ('{}'), ('{}')",
		"INSERT INTO namespaces (manifest) VALUES ('{}') WHERE TRUE", "INSERT INTO namespaces (manifest) VALUES ('{}'); garbage",
	} {
		if stmt, err := Parse(input); err == nil || stmt != nil {
			t.Fatalf("invalid SQL accepted: %s", input)
		}
	}
}
