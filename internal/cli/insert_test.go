package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInsertCLIPreflight(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	for _, input := range []string{
		`INSERT INTO namespaces (manifest) VALUES ('[]')`,
		`INSERT INTO deployments (manifest) VALUES ('{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"web"}}')`,
		`INSERT INTO namespaces (manifest) VALUES ('{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"web","uid":null}}')`,
		`INSERT INTO deployments (manifest) VALUES ('{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"other"}}')`,
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader(input), &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 || stderr.Len() == 0 || calls != 0 {
			t.Fatalf("exit=%d, stdout=%s, stderr=%s, calls=%d", code, &stdout, &stderr, calls)
		}
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--all-namespaces", "--kubeconfig", "missing"}, strings.NewReader(`INSERT INTO namespaces (manifest) VALUES ('{}')`), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "E_NAMESPACE") {
		t.Fatalf("exit=%d, stderr=%s", code, &stderr)
	}
}

func TestInsertCLIProtocolAndResults(t *testing.T) {
	for _, tt := range []struct {
		table, version, kind, path, reason string
		status, exit                       int
	}{
		{"namespaces", "v1", "Namespace", "/api/v1/namespaces", "", 201, 0},
		{"deployments", "apps/v1", "Deployment", "/apis/apps/v1/namespaces/context-ns/deployments", "", 201, 0},
		{"ingresses", "networking.k8s.io/v1", "Ingress", "/apis/networking.k8s.io/v1/namespaces/context-ns/ingresses", "AlreadyExists", 409, 1},
		{"deployments", "apps/v1", "Deployment", "/apis/apps/v1/namespaces/context-ns/deployments", "Invalid", 422, 1},
	} {
		t.Run(tt.table+tt.reason, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if serveDiscovery(w, r) {
					return
				}
				calls++
				if r.Method != http.MethodPost || r.URL.Path != tt.path {
					t.Errorf("request=%s %s", r.Method, r.URL.Path)
				}
				var object map[string]any
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal(data, &object); err != nil {
					t.Error(err)
				}
				metadata := object["metadata"].(map[string]any)
				if object["apiVersion"] != tt.version || object["kind"] != tt.kind || metadata["name"] != "web" {
					t.Errorf("object=%s", data)
				}
				if tt.table == "namespaces" {
					if metadata["namespace"] != nil {
						t.Error("cluster-scoped create sent namespace")
					}
				} else if metadata["namespace"] != "context-ns" {
					t.Error("create missed resolved namespace")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				if tt.reason == "" {
					_, _ = w.Write(data)
				} else {
					fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Status","reason":%q,"message":"sensitive-payload","code":%d}`, tt.reason, tt.status)
				}
			}))
			defer server.Close()
			input := fmt.Sprintf(`INSERT INTO %s (manifest) VALUES ('{"apiVersion":"%s","kind":"%s","metadata":{"name":"web"}}')`, tt.table, tt.version, tt.kind)
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader(input), &stdout, &stderr)
			if code != tt.exit || calls != 1 || stderr.Len() != 0 {
				t.Fatalf("exit=%d, calls=%d, stderr=%s", code, calls, &stderr)
			}
			var result struct {
				AffectedRows int                                                  `json:"affected_rows"`
				FailedRows   int                                                  `json:"failed_rows"`
				Errors       []struct{ Resource, Namespace, Name, Reason string } `json:"errors"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if tt.reason == "" {
				if stdout.String() != "{\"affected_rows\":1}\n" {
					t.Fatalf("output=%s", &stdout)
				}
			} else if result.AffectedRows != 0 || result.FailedRows != 1 || len(result.Errors) != 1 || result.Errors[0].Reason != tt.reason || result.Errors[0].Resource != tt.table || result.Errors[0].Name != "web" || result.Errors[0].Namespace != "context-ns" || strings.Contains(stdout.String(), "sensitive-payload") {
				t.Fatalf("output=%s", &stdout)
			}
		})
	}
}
