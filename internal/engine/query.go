package engine

import (
	"context"
	"fmt"
	"slices"

	"github.com/Palm0palM/kubesql/internal/resource"
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
	name    string
	path    []string
	kind    valueKind
	output  string
	pointer bool
}

type table struct {
	gvr        schema.GroupVersionResource
	namespaced bool
	columns    []column
	kind       string
	verbs      []string
}

var tables = map[string]table{
	"namespaces": {
		gvr: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
		columns: []column{
			{name: "name", path: []string{"metadata", "name"}, kind: stringKind},
			{name: "namespace", kind: stringKind},
		},
	},
	"deployments": {
		gvr:        schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		namespaced: true,
		columns: []column{
			{name: "name", path: []string{"metadata", "name"}, kind: stringKind},
			{name: "namespace", path: []string{"metadata", "namespace"}, kind: stringKind},
			{name: "replicas", path: []string{"spec", "replicas"}, kind: numberKind},
		},
	},
	"ingresses": {
		gvr:        schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		namespaced: true,
		columns: []column{
			{name: "name", path: []string{"metadata", "name"}, kind: stringKind},
			{name: "namespace", path: []string{"metadata", "namespace"}, kind: stringKind},
			{name: "default_backend_service", path: []string{"spec", "defaultBackend", "service", "name"}, kind: stringKind},
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
func Bind(stmt *sql.Statement, resolved ...resource.Descriptor) (*Query, error) {
	t, err := bindTable(stmt, resolved)
	if err != nil {
		return nil, err
	}
	return bindQueryTable(stmt, t)
}

func bindTable(stmt *sql.Statement, resolved []resource.Descriptor) (table, error) {
	t, ok := tables[stmt.Table]
	if len(resolved) != 0 {
		d := resolved[0]
		t = table{gvr: d.GVR, namespaced: d.Namespaced, kind: d.Kind, verbs: d.Verbs}
		for _, known := range tables {
			if known.gvr == d.GVR {
				t.columns = known.columns
			}
		}
		if t.columns == nil {
			t.columns = []column{{name: "name", path: []string{"metadata", "name"}, kind: stringKind}}
		}
		if !slices.ContainsFunc(t.columns, func(c column) bool { return c.name == "namespace" }) {
			path := []string{"metadata", "namespace"}
			if !d.Namespaced {
				path = nil
			}
			t.columns = append(append([]column(nil), t.columns...), column{name: "namespace", path: path, kind: stringKind})
		}
	} else if !ok {
		return table{}, &Error{Code: "E_UNKNOWN_TABLE", Message: fmt.Sprintf("unknown table %q", stmt.Table)}
	}
	t.columns = append(append([]column(nil), t.columns...),
		column{name: "labels", path: []string{"metadata", "labels"}, kind: objectKind},
		column{name: "annotations", path: []string{"metadata", "annotations"}, kind: objectKind},
		column{name: "manifest", kind: objectKind})
	return t, nil
}

func requireVerbs(t table, verbs ...string) error {
	// nil is reserved for offline static bindings/preflight. Discovery supplies a
	// non-nil list, including an empty list when no verbs are advertised.
	if t.verbs == nil {
		return nil
	}
	for _, verb := range verbs {
		if !slices.Contains(t.verbs, verb) {
			return &Error{Code: "E_UNSUPPORTED_VERB", Message: "resource does not advertise required verb " + verb}
		}
	}
	return nil
}

func bindQueryTable(stmt *sql.Statement, t table) (*Query, error) {
	if stmt.Type == "select" {
		if err := requireVerbs(t, "list"); err != nil {
			return nil, err
		}
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
		output := selected.Name
		if selected.Alias != "" {
			output = selected.Alias
		}
		if seen[output] {
			return nil, &Error{Code: "E_DUPLICATE_COLUMN", Message: fmt.Sprintf("duplicate column %q", selected.Name)}
		}
		seen[output] = true
		c, err := resolveColumn(selected.Name, selected.Quoted, t)
		if err != nil {
			return nil, err
		}
		c.output = output
		q.columns = append(q.columns, c)
	}
	return q, nil
}

// Lister is the single backend operation needed by M2.
type Lister interface {
	List(context.Context, schema.GroupVersionResource, string) ([]unstructured.Unstructured, error)
}

func (q *Query) Execute(ctx context.Context, client Lister, namespace string, allNamespaces bool) ([]map[string]any, error) {
	objects, err := q.candidates(ctx, client, namespace, allNamespaces)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(objects))
	for _, object := range objects {
		row := make(map[string]any, len(q.columns))
		for _, c := range q.columns {
			value, err := readColumn(object, c)
			if err != nil {
				return nil, err
			}
			key := c.output
			if key == "" {
				key = c.name
			}
			row[key] = value
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// candidates evaluates the entire WHERE before a caller starts any writes.
func (q *Query) candidates(ctx context.Context, client Lister, namespace string, allNamespaces bool) ([]unstructured.Unstructured, error) {
	scope := ""
	if q.table.namespaced && !allNamespaces {
		scope = namespace
	}
	objects, err := client.List(ctx, q.table.gvr, scope)
	if err != nil {
		return nil, err
	}
	matchedObjects := make([]unstructured.Unstructured, 0, len(objects))
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
		matchedObjects = append(matchedObjects, object)
	}
	return matchedObjects, nil
}
