//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestHardMetrics(t *testing.T) {
	ctx, binary, args := prepareFixture(t, "hard-metrics", "sql-hard-metrics")
	client := testClient(t)
	waitCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	pods := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Namespace("sql-hard-metrics")
	if err := wait.PollUntilContextCancel(waitCtx, time.Second, true, func(ctx context.Context) (bool, error) {
		pod, err := pods.Get(ctx, "measure", metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		conditions, _, err := unstructured.NestedSlice(pod.Object, "status", "conditions")
		if err != nil {
			return false, err
		}
		for _, raw := range conditions {
			c, ok := raw.(map[string]any)
			if ok && c["type"] == "Ready" && c["status"] == "True" {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatalf("Pod Ready within 180s: %v", err)
	}
	metrics := client.Resource(schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}).Namespace("sql-hard-metrics")
	if err := wait.PollUntilContextCancel(waitCtx, time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := metrics.Get(ctx, "measure", metav1.GetOptions{})
		if apierrors.IsNotFound(err) || apierrors.IsServiceUnavailable(err) {
			return false, nil
		}
		return err == nil, err
	}); err != nil {
		t.Fatalf("metric sample within 180s: %v", err)
	}
	for _, tt := range []struct{ file, name string }{{"pods.sql", "measure"}, {"nodes.sql", "kubesql-test"}} {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = bytes.NewReader(readFixture(t, "hard-metrics", tt.file))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil || stderr.Len() != 0 {
			t.Fatalf("metrics CLI failed: %v, stderr=%s", err, &stderr)
		}
		var rows []map[string]any
		decoder := json.NewDecoder(&stdout)
		decoder.UseNumber()
		if err := decoder.Decode(&rows); err != nil || len(rows) != 1 || rows[0]["name"] != tt.name || len(rows[0]) != 3 {
			t.Fatalf("unexpected metric row=%v,error=%v", rows, err)
		}
		for _, field := range []string{"cpu_millicores", "memory_bytes"} {
			number, ok := rows[0][field].(json.Number)
			if !ok {
				t.Fatalf("%s must be a JSON integer", field)
			}
			n, err := number.Int64()
			if err != nil || n < 0 {
				t.Fatalf("invalid %s=%v", field, number)
			}
		}
		t.Logf("%s real sample: %v", tt.file, rows[0])
	}
	// Numeric WHERE and scope still use the shared query path.
	executeSQL(t, ctx, binary, args, `SELECT name, namespace FROM pod_metrics WHERE name = 'measure' AND cpu_millicores >= 0 AND memory_bytes >= 0`, `[{"name":"measure","namespace":"sql-hard-metrics"}]`)
	executeSQL(t, ctx, binary, args, `SELECT name FROM pod_metrics WHERE name = 'absent'`, `[]`)
	allArgs := append(append([]string{}, args...), "--all-namespaces")
	executeSQL(t, ctx, binary, allArgs, `SELECT name FROM pod_metrics WHERE namespace = 'sql-hard-metrics' AND name = 'measure'`, `[{"name":"measure"}]`)
}
