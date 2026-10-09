package sql

import "testing"

func TestQuotedNamesAliasesAndCast(t *testing.T) {
	stmt, err := Parse(`SELECT name, "/spec/replicas" AS replicas, "/data/a~1b" AS "Mixed" FROM "apps/v1/statefulsets" WHERE "/spec/replicas" >= 0`)
	if err != nil || !stmt.TableQuoted || stmt.Table != "apps/v1/statefulsets" || !stmt.Columns[1].Quoted || stmt.Columns[1].Alias != "replicas" || stmt.Columns[2].Alias != "Mixed" {
		t.Fatalf("statement=%+v, error=%v", stmt, err)
	}
	stmt, err = Parse(`UPDATE "v1/configmaps" SET "/data" = CAST('{"owner":"alice''s"}' AS JSON), "/metadata/labels/old" = NULL WHERE name = 'settings'`)
	if err != nil || !stmt.Assignments[0].Quoted {
		t.Fatalf("statement=%+v, error=%v", stmt, err)
	}
	cast, ok := stmt.Assignments[0].Value.(*JSONCast)
	if !ok || cast.Text != `{"owner":"alice's"}` {
		t.Fatalf("cast=%+v", cast)
	}
	stmt, err = Parse(`SELECT "/data/a" AS "a""b" FROM "v1/configmaps"`)
	if err != nil || stmt.Columns[0].Alias != `a"b` {
		t.Fatalf("statement=%+v, error=%v", stmt, err)
	}
	for _, input := range []string{
		`SELECT name AS "" FROM configmaps`,
		`SELECT "unfinished FROM configmaps`, `SELECT name AS FROM configmaps`,
		`UPDATE configmaps SET "/data" = CAST('{}' AS TEXT) WHERE TRUE`,
		`UPDATE configmaps SET "/data" = CAST(3 AS JSON) WHERE TRUE`,
		`INSERT INTO configmaps (manifest AS x) VALUES ('{}')`,
	} {
		if _, err := Parse(input); err == nil {
			t.Fatalf("invalid SQL accepted: %s", input)
		}
	}
}
