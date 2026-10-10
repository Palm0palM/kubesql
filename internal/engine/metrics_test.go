package engine

import (
	"context"
	"testing"

	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type stubMetrics struct {
	pods      bool
	namespace string
	rows      []unstructured.Unstructured
}

func (m *stubMetrics) Metrics(_ context.Context, pods bool, namespace string) ([]unstructured.Unstructured, error) {
	m.pods, m.namespace = pods, namespace
	return m.rows, nil
}

func TestMetricsSharedScopeAndProjection(t *testing.T) {
	for _, tt := range []struct {
		input     string
		pods, all bool
		scope     string
		keys      int
	}{
		{`SELECT * FROM pod_metrics WHERE cpu_millicores >= 0`, true, false, "chosen", 4},
		{`SELECT name AS identity FROM pod_metrics`, true, true, "", 1},
		{`SELECT * FROM node_metrics`, false, false, "", 3},
	} {
		stmt, err := sql.Parse(tt.input)
		if err != nil {
			t.Fatal(err)
		}
		q, err := BindMetrics(stmt)
		if err != nil {
			t.Fatal(err)
		}
		backend := &stubMetrics{rows: []unstructured.Unstructured{{Object: map[string]any{"metadata": map[string]any{"name": "measure", "namespace": "chosen"}, "cpu_millicores": int64(1), "memory_bytes": int64(3)}}}}
		rows, err := q.ExecuteMetrics(context.Background(), backend, "chosen", tt.all)
		if err != nil || len(rows) != 1 || len(rows[0]) != tt.keys || backend.pods != tt.pods || backend.namespace != tt.scope {
			t.Fatalf("rows=%v, scope=%s, pods=%v,error=%v", rows, backend.namespace, backend.pods, err)
		}
		rows, err = q.ExecuteMetrics(context.Background(), &stubMetrics{}, "chosen", tt.all)
		if err != nil || rows == nil || len(rows) != 0 {
			t.Fatalf("empty rows=%v,error=%v", rows, err)
		}
	}
}
