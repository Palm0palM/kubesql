package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsCLIReadOnlyAndSchema(t *testing.T) {
	for _, input := range []string{
		`UPDATE pod_metrics SET cpu_millicores = 0 WHERE TRUE`, `DELETE FROM node_metrics WHERE TRUE`,
		`INSERT INTO pod_metrics (manifest) VALUES ('{}')`, `SELECT typo FROM pod_metrics`,
		`SELECT namespace FROM node_metrics`, `SELECT manifest FROM pod_metrics`,
		`SELECT "/usage/cpu" FROM pod_metrics`, `SELECT name FROM pod_metrics WHERE cpu_millicores = '1'`,
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"--kubeconfig", "missing"}, strings.NewReader(input), &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 || strings.Contains(stderr.String(), "E_CONFIG") {
			t.Fatalf("exit=%d,stderr=%s", code, &stderr)
		}
	}
}

func TestMetricsCLIFilterProjectionAndErrors(t *testing.T) {
	for _, tt := range []struct {
		input, path, body, want string
		status, exit            int
	}{
		{`SELECT name, cpu_millicores, memory_bytes FROM pod_metrics WHERE name = 'measure' AND cpu_millicores >= 150`, "/apis/metrics.k8s.io/v1beta1/namespaces/context-ns/pods", `{"apiVersion":"metrics.k8s.io/v1beta1","kind":"PodMetricsList","items":[{"metadata":{"name":"measure","namespace":"context-ns"},"containers":[{"usage":{"cpu":"100m","memory":"64Mi"}},{"usage":{"cpu":"50m","memory":"16Mi"}}]}]}`, "[{\"cpu_millicores\":150,\"memory_bytes\":83886080,\"name\":\"measure\"}]\n", 200, 0},
		{`SELECT * FROM node_metrics`, "/apis/metrics.k8s.io/v1beta1/nodes", `{"apiVersion":"metrics.k8s.io/v1beta1","kind":"NodeMetricsList","items":[{"metadata":{"name":"test-node"},"usage":{"cpu":"250m","memory":"512Mi"}}]}`, "[{\"cpu_millicores\":250,\"memory_bytes\":536870912,\"name\":\"test-node\"}]\n", 200, 0},
		{`SELECT name FROM pod_metrics`, "/apis/metrics.k8s.io/v1beta1/namespaces/context-ns/pods", `{"apiVersion":"metrics.k8s.io/v1beta1","kind":"PodMetricsList","items":[]}`, "[]\n", 200, 0},
		{`SELECT name FROM pod_metrics`, "/apis/metrics.k8s.io/v1beta1/namespaces/context-ns/pods", `{"apiVersion":"v1","kind":"Status","reason":"NotFound","code":404}`, "E_METRICS_UNAVAILABLE", 404, 1},
		{`SELECT name FROM node_metrics`, "/apis/metrics.k8s.io/v1beta1/nodes", `{"apiVersion":"v1","kind":"Status","reason":"Forbidden","message":"secret-field-value","code":403}`, "Forbidden", 403, 1},
	} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path != tt.path {
				t.Errorf("unexpected path=%s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tt.status)
			fmt.Fprint(w, tt.body)
		}))
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader(tt.input), &stdout, &stderr)
		server.Close()
		if code != tt.exit || calls != 1 {
			t.Fatalf("exit=%d,calls=%d,stderr=%s", code, calls, &stderr)
		}
		if tt.exit == 0 {
			if stdout.String() != tt.want || stderr.Len() != 0 {
				t.Fatalf("stdout=%s,stderr=%s", &stdout, &stderr)
			}
		} else if stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.want) || strings.Contains(stderr.String(), "secret-field-value") {
			t.Fatalf("stdout=%s,stderr=%s", &stdout, &stderr)
		}
	}
}
