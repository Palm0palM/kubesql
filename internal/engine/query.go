package engine

import (
	"context"
	"fmt"

	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Error is a semantic error, detected before accessing Kubernetes.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type column struct {
	name string
	path []string
}

type table struct {
	gvr        schema.GroupVersionResource
	namespaced bool
	columns    []column
}

var tables = map[string]table{
	"namespaces": {
		gvr:     schema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
		columns: []column{{"name", []string{"metadata", "name"}}},
	},
	"deployments": {
		gvr:        schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		namespaced: true,
		columns: []column{
			{"name", []string{"metadata", "name"}},
			{"namespace", []string{"metadata", "namespace"}},
			{"replicas", []string{"spec", "replicas"}},
		},
	},
	"ingresses": {
		gvr:        schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		namespaced: true,
		columns: []column{
			{"name", []string{"metadata", "name"}},
			{"namespace", []string{"metadata", "namespace"}},
			{"default_backend_service", []string{"spec", "defaultBackend", "service", "name"}},
		},
	},
}

// Query contains a bound table and projection; it does not hold a client.
type Query struct {
	table   table
	columns []column
}

// Bind validates every column, including for an empty result, without API calls.
func Bind(stmt *sql.SelectStatement) (*Query, error) {
	t, ok := tables[stmt.Table]
	if !ok {
		return nil, &Error{Code: "E_UNKNOWN_TABLE", Message: fmt.Sprintf("unknown table %q", stmt.Table)}
	}
	q := &Query{table: t}
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
		row := make(map[string]any, len(q.columns))
		for _, c := range q.columns {
			value, _, err := unstructured.NestedFieldNoCopy(object.Object, c.path...)
			if err != nil {
				return nil, fmt.Errorf("cannot read column %q: invalid resource field structure", c.name)
			}
			row[c.name] = value
		}
		rows = append(rows, row)
	}
	return rows, nil
}
