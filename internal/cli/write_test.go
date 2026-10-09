package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWritePreflightNeverCallsAPI(t *testing.T) {
	for _, input := range []string{
		"UPDATE deployments SET replicas = 1", "DELETE FROM deployments",
		"UPDATE deployments SET replicas = -1 WHERE TRUE",
		"UPDATE deployments SET name = 'new' WHERE TRUE",
		`UPDATE namespaces SET annotations = '{"owner":3}' WHERE TRUE`,
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"--kubeconfig", "nonexistent-kubeconfig"}, strings.NewReader(input), &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 || strings.Contains(stderr.String(), "E_CONFIG") {
			t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, &stdout, &stderr)
		}
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--all-namespaces", "--kubeconfig", "missing"}, strings.NewReader("DELETE FROM deployments WHERE TRUE"), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "E_NAMESPACE") {
		t.Fatalf("unsafe write allowed: %d, %s", code, &stderr)
	}
}

func TestWriteCLIContinuesAfterAPIFailure(t *testing.T) {
	for _, operation := range []struct{ input, method string }{
		{"UPDATE deployments SET replicas = 2 WHERE TRUE", http.MethodPatch},
		{"DELETE FROM deployments WHERE TRUE", http.MethodDelete},
	} {
		t.Run(operation.method, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if serveDiscovery(w, r) {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"DeploymentList","items":[{"metadata":{"name":"bad","namespace":"context-ns","resourceVersion":"12"},"spec":{"replicas":0}},{"metadata":{"name":"good","namespace":"context-ns","resourceVersion":"13"},"spec":{"replicas":0}}]}`)
					return
				}
				writes++
				if r.Method != operation.method || !strings.HasPrefix(r.URL.Path, "/apis/apps/v1/namespaces/context-ns/deployments/") {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if strings.HasSuffix(r.URL.Path, "/bad") {
					w.WriteHeader(http.StatusConflict)
					fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","reason":"Conflict","message":"sensitive-resource-payload","code":409}`)
				} else {
					fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"good"}}`)
				}
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader(operation.input), &stdout, &stderr)
			var result struct {
				AffectedRows int                                                  `json:"affected_rows"`
				FailedRows   int                                                  `json:"failed_rows"`
				Errors       []struct{ Resource, Namespace, Name, Reason string } `json:"errors"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || code != 1 || stderr.Len() != 0 || writes != 2 || result.AffectedRows != 1 || result.FailedRows != 1 || len(result.Errors) != 1 {
				t.Fatalf("exit = %d, stdout = %s, stderr = %s, writes = %d, error = %v", code, &stdout, &stderr, writes, err)
			}
			if result.Errors[0].Name != "bad" || result.Errors[0].Namespace != "context-ns" || result.Errors[0].Resource != "deployments" || result.Errors[0].Reason != "Conflict" || strings.Contains(stdout.String(), "sensitive-resource-payload") {
				t.Fatalf("wrong error identity or leaked payload: %s", &stdout)
			}
		})
	}
}
