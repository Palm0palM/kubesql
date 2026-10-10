package engine

import (
	"context"

	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func IsMetrics(stmt *sql.Statement) bool {
	return !stmt.TableQuoted && (stmt.Table == "pod_metrics" || stmt.Table == "node_metrics")
}

// BindMetrics has a fixed, read-only scalar schema, not a Kubernetes CRUD table.
func BindMetrics(stmt *sql.Statement) (*Query, error) {
	if stmt.Type != "select" {
		return nil, &Error{Code: "E_READ_ONLY", Message: "metrics tables only support SELECT"}
	}
	t := table{namespaced: stmt.Table == "pod_metrics", gvr: schema.GroupVersionResource{Resource: stmt.Table}, columns: []column{
		{name: "name", path: []string{"metadata", "name"}, kind: stringKind},
	}}
	if t.namespaced {
		t.columns = append(t.columns, column{name: "namespace", path: []string{"metadata", "namespace"}, kind: stringKind})
	}
	t.columns = append(t.columns,
		column{name: "cpu_millicores", path: []string{"cpu_millicores"}, kind: numberKind},
		column{name: "memory_bytes", path: []string{"memory_bytes"}, kind: numberKind})
	// Do not expose raw metric documents through Pointer/manifest: these are
	// virtual rows with only the documented columns, not general resources.
	for _, c := range stmt.Columns {
		if c.Quoted && (c.Name == "" || c.Name[0] == '/') {
			return nil, &Error{Code: "E_UNKNOWN_COLUMN", Message: "metrics tables do not expose JSON Pointer columns"}
		}
	}
	if err := metricsWhere(stmt.Where); err != nil {
		return nil, err
	}
	return bindQueryTable(stmt, t)
}

func metricsWhere(expr sql.Expression) error {
	switch e := expr.(type) {
	case *sql.ColumnReference:
		if e.Quoted && (e.Name == "" || e.Name[0] == '/') {
			return &Error{Code: "E_UNKNOWN_COLUMN", Message: "metrics tables do not expose JSON Pointer columns"}
		}
	case *sql.UnaryExpression:
		return metricsWhere(e.Operand)
	case *sql.BinaryExpression:
		if err := metricsWhere(e.Left); err != nil {
			return err
		}
		return metricsWhere(e.Right)
	}
	return nil
}

type MetricsReader interface {
	Metrics(context.Context, bool, string) ([]unstructured.Unstructured, error)
}

// metricsLister adapts already summarized rows to the existing filter/projection.
type metricsLister struct {
	reader MetricsReader
	pods   bool
}

func (m metricsLister) List(ctx context.Context, _ schema.GroupVersionResource, namespace string) ([]unstructured.Unstructured, error) {
	return m.reader.Metrics(ctx, m.pods, namespace)
}

func (q *Query) ExecuteMetrics(ctx context.Context, reader MetricsReader, namespace string, all bool) ([]map[string]any, error) {
	return q.Execute(ctx, metricsLister{reader: reader, pods: q.table.namespaced}, namespace, all)
}
