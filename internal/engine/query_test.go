package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type stubLister struct {
	objects   []unstructured.Unstructured
	namespace string
	gvr       schema.GroupVersionResource
	err       error
}

func (s *stubLister) List(_ context.Context, gvr schema.GroupVersionResource, namespace string) ([]unstructured.Unstructured, error) {
	s.namespace, s.gvr = namespace, gvr
	return s.objects, s.err
}

func bindQuery(t *testing.T, input string) *Query {
	t.Helper()
	stmt, err := sql.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	q, err := Bind(stmt)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestQueryScopeAndProjection(t *testing.T) {
	tests := []struct {
		table, scope string
		all          bool
		want         map[string]any
	}{
		{"namespaces", "", false, map[string]any{"name": "web"}},
		{"namespaces", "", true, map[string]any{"name": "web"}},
		{"deployments", "selected", false, map[string]any{"name": "web", "namespace": "selected", "replicas": int64(2)}},
		{"deployments", "", true, map[string]any{"name": "web", "namespace": "selected", "replicas": int64(2)}},
		{"ingresses", "selected", false, map[string]any{"name": "web", "namespace": "selected", "default_backend_service": "backend"}},
	}
	for _, tt := range tests {
		t.Run(tt.table+tt.scope, func(t *testing.T) {
			q := bindQuery(t, "SELECT * FROM "+tt.table)
			tt.want["labels"], tt.want["annotations"] = nil, nil
			backend := &stubLister{objects: []unstructured.Unstructured{{Object: map[string]any{
				"metadata": map[string]any{"name": "web", "namespace": "selected"},
				"spec": map[string]any{"replicas": int64(2), "defaultBackend": map[string]any{
					"service": map[string]any{"name": "backend"},
				}},
			}}}}
			tt.want["manifest"] = backend.objects[0].Object
			rows, err := q.Execute(context.Background(), backend, "selected", tt.all)
			if err != nil || !reflect.DeepEqual(rows, []map[string]any{tt.want}) {
				t.Fatalf("rows = %#v, error = %v", rows, err)
			}
			if backend.namespace != tt.scope || backend.gvr.Resource != tt.table {
				t.Fatalf("route = %v, namespace = %q", backend.gvr, backend.namespace)
			}
		})
	}
}

func TestIngressMissingServiceIsNull(t *testing.T) {
	for _, spec := range []map[string]any{
		{},
		{"rules": []any{map[string]any{"backend": "not-the-default"}}},
		{"defaultBackend": map[string]any{"resource": map[string]any{"name": "not-a-service"}}},
	} {
		backend := &stubLister{objects: []unstructured.Unstructured{{Object: map[string]any{"spec": spec}}}}
		rows, err := bindQuery(t, "SELECT default_backend_service FROM ingresses").Execute(context.Background(), backend, "default", false)
		if err != nil || rows[0]["default_backend_service"] != nil {
			t.Fatalf("rows = %#v, error = %v", rows, err)
		}
		encoded, err := json.Marshal(rows)
		if err != nil || string(encoded) != `[{"default_backend_service":null}]` {
			t.Fatalf("JSON = %s, error = %v", encoded, err)
		}
	}
}

func TestBindRejectsUnknownAndDuplicateNames(t *testing.T) {
	for input, code := range map[string]string{
		"SELECT name FROM deploy":            "E_UNKNOWN_TABLE",
		"SELECT typo FROM deployments":       "E_UNKNOWN_COLUMN",
		"SELECT namespace FROM namespaces":   "E_UNKNOWN_COLUMN",
		"SELECT name, name FROM deployments": "E_DUPLICATE_COLUMN",
	} {
		stmt, err := sql.Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Bind(stmt)
		var detail *Error
		if !errors.As(err, &detail) || detail.Code != code {
			t.Fatalf("%q: error = %v, want %s", input, err, code)
		}
	}
}

func TestEmptyRowsAndBackendError(t *testing.T) {
	q := bindQuery(t, "SELECT name FROM deployments")
	rows, err := q.Execute(context.Background(), &stubLister{}, "default", false)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty rows = %#v, error = %v", rows, err)
	}
	want := errors.New("backend failure")
	_, err = q.Execute(context.Background(), &stubLister{err: want}, "default", false)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
