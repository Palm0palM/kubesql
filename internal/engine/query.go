package engine

import (
	"context"
	"fmt"

	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Error is a semantic/type error; known schema errors precede API requests.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type column struct {
	name string
	path []string
	kind valueKind
}

type table struct {
	gvr        schema.GroupVersionResource
	namespaced bool
	columns    []column
}

var tables = map[string]table{
	"namespaces": {
		gvr:     schema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
		columns: []column{{"name", []string{"metadata", "name"}, stringKind}},
	},
	"deployments": {
		gvr:        schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		namespaced: true,
		columns: []column{
			{"name", []string{"metadata", "name"}, stringKind},
			{"namespace", []string{"metadata", "namespace"}, stringKind},
			{"replicas", []string{"spec", "replicas"}, numberKind},
		},
	},
	"ingresses": {
		gvr:        schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		namespaced: true,
		columns: []column{
			{"name", []string{"metadata", "name"}, stringKind},
			{"namespace", []string{"metadata", "namespace"}, stringKind},
			{"default_backend_service", []string{"spec", "defaultBackend", "service", "name"}, stringKind},
		},
	},
}

// Query contains a bound table and projection; it does not hold a client.
type Query struct {
	table   table
	columns []column
	where   *boundExpression
}

// Bind validates every column, including for an empty result, without API calls.
func Bind(stmt *sql.SelectStatement) (*Query, error) {
	t, ok := tables[stmt.Table]
	if !ok {
		return nil, &Error{Code: "E_UNKNOWN_TABLE", Message: fmt.Sprintf("unknown table %q", stmt.Table)}
	}
	q := &Query{table: t}
	if stmt.Where != nil {
		where, err := bindExpression(stmt.Where, t)
		if err != nil {
			return nil, err
		}
		if !booleanKind(where.kind) {
			return nil, typeError("WHERE requires a boolean expression")
		}
		q.where = where
	}
	if len(stmt.Columns) == 1 && stmt.Columns[0].Type == "star" {
		q.columns = t.columns
		return q, nil
	}
	seen := make(map[string]bool)
	for _, selected := range stmt.Columns {
		if seen[selected.Name] {
			return nil, &Error{Code: "E_DUPLICATE_COLUMN", Message: fmt.Sprintf("duplicate column %q", selected.Name)}
		}
		seen[selected.Name] = true
		found := false
		for _, c := range t.columns {
			if c.name == selected.Name {
				q.columns = append(q.columns, c)
				found = true
				break
			}
		}
		if !found {
			return nil, &Error{Code: "E_UNKNOWN_COLUMN", Message: fmt.Sprintf("unknown column %q for table %q", selected.Name, stmt.Table)}
		}
	}
	return q, nil
}

// Lister is the single backend operation needed by M2.
type Lister interface {
	List(context.Context, schema.GroupVersionResource, string) ([]unstructured.Unstructured, error)
}

func (q *Query) Execute(ctx context.Context, client Lister, namespace string, allNamespaces bool) ([]map[string]any, error) {
	scope := ""
	if q.table.namespaced && !allNamespaces {
		scope = namespace
	}
	objects, err := client.List(ctx, q.table.gvr, scope)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(objects))
	for _, object := range objects {
		if q.where != nil {
			result, err := q.where.evaluate(object)
			if err != nil {
				return nil, err
			}
			matched, err := result.asTruth()
			if err != nil {
				return nil, err
			}
			if matched != trueTruth {
				continue
			}
		}
		row := make(map[string]any, len(q.columns))
		for _, c := range q.columns {
			value, err := readColumn(object, c)
			if err != nil {
				return nil, err
			}
			row[c.name] = value
		}
		rows = append(rows, row)
	}
	return rows, nil
}
