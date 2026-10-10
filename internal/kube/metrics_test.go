package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	shared "github.com/Palm0palM/kubesql/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func metricObject(t *testing.T, text string) unstructured.Unstructured {
	t.Helper()
	var o unstructured.Unstructured
	if err := json.Unmarshal([]byte(text), &o.Object); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestMetricQuantitySummaries(t *testing.T) {
	for _, tt := range []struct {
		text        string
		pods        bool
		cpu, memory int64
	}{
		{`{"metadata":{"name":"measure","namespace":"test"},"containers":[{"usage":{"cpu":"100m","memory":"64Mi"}},{"usage":{"cpu":"50m","memory":"16Mi"}}]}`, true, 150, 83886080},
		{`{"metadata":{"name":"test-node"},"usage":{"cpu":"250m","memory":"512Mi"}}`, false, 250, 536870912},
		{`{"metadata":{"name":"measure","namespace":"test"},"containers":[{"usage":{"cpu":"400u","memory":"1"}},{"usage":{"cpu":"400u","memory":"2"}}]}`, true, 1, 3},
		{`{"metadata":{"name":"test-node"},"usage":{"cpu":"0","memory":"0"}}`, false, 0, 0},
	} {
		row, err := summarizeMetric(metricObject(t, tt.text), tt.pods)
		if err != nil || row.Object["cpu_millicores"] != tt.cpu || row.Object["memory_bytes"] != tt.memory {
			t.Fatalf("row=%v, error=%v", row.Object, err)
		}
	}
}

func TestMetricInvalidSamplesNeverBecomeZero(t *testing.T) {
	for _, text := range []string{
		`{"metadata":{"name":"node"}}`,
		`{"metadata":{"name":"node"},"usage":{"cpu":"1m"}}`,
		`{"metadata":{"name":"node"},"usage":{"cpu":"bad-secret","memory":"1Mi"}}`,
		`{"metadata":{"name":"node"},"usage":{"cpu":"-1","memory":"1Mi"}}`,
		`{"metadata":{"name":"node"},"usage":{"cpu":"1e30","memory":"1Mi"}}`,
		`{"metadata":{"name":"node"},"usage":{"cpu":"1m","memory":"1e30"}}`,
		`{"metadata":{"name":"node"},"usage":{"cpu":"1m","memory":"-1"}}`,
	} {
		if _, err := summarizeMetric(metricObject(t, text), false); err == nil {
			t.Fatalf("invalid sample accepted: %s", text)
		}
	}
	if _, err := summarizeMetric(metricObject(t, `{"metadata":{"name":"pod","namespace":"test"},"containers":[]}`), true); err == nil {
		t.Fatal("empty containers invented zero")
	}
}

func TestMetricsHTTPPathsAndFailures(t *testing.T) {
	for _, tt := range []struct {
		pods             bool
		ns, path, reason string
		status           int
	}{
		{true, "chosen", "/apis/metrics.k8s.io/v1beta1/namespaces/chosen/pods", "", 200},
		{true, "", "/apis/metrics.k8s.io/v1beta1/pods", "", 200},
		{false, "unrelated", "/apis/metrics.k8s.io/v1beta1/nodes", "", 200},
		{true, "chosen", "/apis/metrics.k8s.io/v1beta1/namespaces/chosen/pods", "NotFound", 404},
		{true, "chosen", "/apis/metrics.k8s.io/v1beta1/namespaces/chosen/pods", "ServiceUnavailable", 503},
		{true, "chosen", "/apis/metrics.k8s.io/v1beta1/namespaces/chosen/pods", "Forbidden", 403},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != tt.path {
				t.Errorf("request=%s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tt.status)
			if tt.status == 200 {
				fmt.Fprint(w, `{"apiVersion":"metrics.k8s.io/v1beta1","kind":"PodMetricsList","items":[]}`)
			} else {
				fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Status","reason":%q,"message":"sensitive-payload","code":%d}`, tt.reason, tt.status)
			}
		}))
		c, _, err := Connect(context.Background(), Options{Kubeconfig: writeConfig(t, server.URL)})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := c.Metrics(context.Background(), tt.pods, tt.ns)
		server.Close()
		if tt.status == 200 {
			if err != nil || rows == nil || len(rows) != 0 {
				t.Fatalf("rows=%v,error=%v", rows, err)
			}
		} else if tt.status == 403 {
			if !apierrors.IsForbidden(err) {
				t.Fatalf("Forbidden lost: %v", err)
			}
		} else {
			var detail *shared.Error
			if !errors.As(err, &detail) || detail.Code != "E_METRICS_UNAVAILABLE" {
				t.Fatalf("error=%v", err)
			}
		}
	}
}
