package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func evaluateWhere(t *testing.T, input string) truth {
	t.Helper()
	q := bindQuery(t, "SELECT name FROM deployments WHERE "+input)
	result, err := q.where.evaluate(unstructured.Unstructured{Object: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := result.asTruth()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCompleteThreeValuedLogic(t *testing.T) {
	values := []string{"NULL", "FALSE", "TRUE"}
	wantAnd := [][]truth{
		{unknown, falseTruth, unknown},
		{falseTruth, falseTruth, falseTruth},
		{unknown, falseTruth, trueTruth},
	}
	wantOr := [][]truth{
		{unknown, unknown, trueTruth},
		{unknown, falseTruth, trueTruth},
		{trueTruth, trueTruth, trueTruth},
	}
	for i, left := range values {
		for j, right := range values {
			for op, want := range map[string]truth{"AND": wantAnd[i][j], "OR": wantOr[i][j]} {
				input := left + " " + op + " " + right
				if got := evaluateWhere(t, input); got != want {
					t.Fatalf("%s = %v, want %v", input, got, want)
				}
			}
		}
	}
	for input, want := range map[string]truth{"NOT TRUE": falseTruth, "NOT FALSE": trueTruth, "NOT NULL": unknown} {
		if got := evaluateWhere(t, input); got != want {
			t.Fatalf("%s = %v, want %v", input, got, want)
		}
	}
}

func TestScalarComparisons(t *testing.T) {
	for input, want := range map[string]truth{
		"10 > 2": trueTruth, "10 < 2": falseTruth, "2 >= 2": trueTruth,
		"2 <= 2": trueTruth, "2 <> 3": trueTruth, "2 <> 2": falseTruth,
		"-2 < -1.5": trueTruth, "0.1 = .10": trueTruth, "+02 = 2.0": trueTruth,
		"9007199254740993 > 9007199254740992":    trueTruth,
		"9007199254740993.01 > 9007199254740993": trueTruth,
		"'it''s' = 'it''s'":                      trueTruth, "'a' < 'b'": trueTruth,
		"TRUE = FALSE": falseTruth, "FALSE <> TRUE": trueTruth,
		"NULL = NULL": unknown, "NULL <> 2": unknown, "NOT (NULL = 2)": unknown,
		"NULL IS NULL": trueTruth, "NULL IS NOT NULL": falseTruth,
		"FALSE IS NULL": falseTruth, "(1 = NULL) IS NULL": trueTruth,
		"(1 = NULL) IS NOT NULL": falseTruth,
	} {
		if got := evaluateWhere(t, input); got != want {
			t.Fatalf("%s = %v, want %v", input, got, want)
		}
	}
}

func TestWhereFiltersBeforeProjection(t *testing.T) {
	objects := []unstructured.Unstructured{
		{Object: map[string]any{"metadata": map[string]any{"name": "web", "namespace": "web"}, "spec": map[string]any{"replicas": int64(1)}}},
		{Object: map[string]any{"metadata": map[string]any{"name": "worker", "namespace": "test"}, "spec": map[string]any{"replicas": int64(3)}}},
	}
	for input, names := range map[string][]string{
		"name = 'web' OR name = 'worker' AND replicas >= 3":   {"web", "worker"},
		"(name = 'web' OR name = 'worker') AND replicas >= 3": {"worker"},
		"name = namespace": {"web"}, "NOT replicas >= 3": {"web"},
		"replicas = NULL": {}, "NULL": {}, "FALSE": {},
	} {
		q := bindQuery(t, "SELECT name FROM deployments WHERE "+input)
		rows, err := q.Execute(context.Background(), &stubLister{objects: objects}, "test", false)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(rows))
		for _, row := range rows {
			if len(row) != 1 {
				t.Fatalf("WHERE-only column leaked into projection: %v", row)
			}
			got = append(got, row["name"].(string))
		}
		if !reflect.DeepEqual(got, names) {
			t.Fatalf("%s: names = %v, want %v", input, got, names)
		}
	}
}

func TestWhereNullFieldsAndRuntimeTypeErrors(t *testing.T) {
	q := bindQuery(t, "SELECT name FROM ingresses WHERE default_backend_service IS NULL")
	object := unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "internal"}, "spec": map[string]any{"rules": []any{}}}}
	rows, err := q.Execute(context.Background(), &stubLister{objects: []unstructured.Unstructured{object}}, "test", false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("missing field: rows = %v, error = %v", rows, err)
	}
	q = bindQuery(t, "SELECT name FROM deployments WHERE replicas >= 3")
	for _, raw := range []any{"3", map[string]any{}, []any{}, true} {
		object := unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"replicas": raw}}}
		rows, err := q.Execute(context.Background(), &stubLister{objects: []unstructured.Unstructured{object}}, "test", false)
		var detail *Error
		if rows != nil || !errors.As(err, &detail) || detail.Code != "E_TYPE" {
			t.Fatalf("bad type: rows = %v, error = %v", rows, err)
		}
	}
}

func TestWhereSemanticErrorsBeforeList(t *testing.T) {
	for input, code := range map[string]string{
		"typo IS NULL": "E_UNKNOWN_COLUMN", "TRUE OR typo = 1": "E_UNKNOWN_COLUMN",
		"replicas = '3'": "E_TYPE", "name = 2": "E_TYPE", "name = replicas": "E_TYPE",
		"TRUE OR replicas = '3'": "E_TYPE", "1 AND TRUE": "E_TYPE",
		"NOT name": "E_TYPE", "name": "E_TYPE", "42": "E_TYPE",
	} {
		stmt, err := sql.Parse("SELECT name FROM deployments WHERE " + input)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Bind(stmt)
		var detail *Error
		if !errors.As(err, &detail) || detail.Code != code {
			t.Fatalf("%s: error = %v, want %s", input, err, code)
		}
	}
}

func TestResourceIntegerPrecision(t *testing.T) {
	q := bindQuery(t, "SELECT name FROM deployments WHERE replicas > 9007199254740992")
	object := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "precise"},
		"spec":     map[string]any{"replicas": int64(9007199254740993)},
	}}
	rows, err := q.Execute(context.Background(), &stubLister{objects: []unstructured.Unstructured{object}}, "test", false)
	if err != nil || len(rows) != 1 || rows[0]["name"] != "precise" {
		t.Fatalf("integer comparison: rows = %v, error = %v", rows, err)
	}
}
