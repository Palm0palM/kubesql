package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configFile(t *testing.T, server string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	content := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: test
clusters:
- name: cluster
  cluster:
    server: %s
users:
- name: user
  user: {}
contexts:
- name: test
  context:
    cluster: cluster
    user: user
    namespace: context-ns
`, server)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCLIRoutingAndJSON(t *testing.T) {
	for _, tt := range []struct {
		query, path, response, want string
		flags                       []string
	}{
		{"SELECT name, replicas FROM deployments;", "/apis/apps/v1/namespaces/context-ns/deployments", `{"metadata":{"name":"web"},"spec":{"replicas":2}}`, `[{"name":"web","replicas":2}]`, nil},
		{"SELECT name FROM deployments;", "/apis/apps/v1/namespaces/chosen/deployments", "", `[]`, []string{"--namespace", "chosen"}},
		{"SELECT name FROM deployments;", "/apis/apps/v1/deployments", "", `[]`, []string{"--all-namespaces"}},
		{"SELECT name FROM namespaces;", "/api/v1/namespaces", `{"metadata":{"name":"default"}}`, `[{"name":"default"}]`, []string{"--namespace", "unrelated"}},
		{"SELECT * FROM ingresses;", "/apis/networking.k8s.io/v1/namespaces/context-ns/ingresses", `{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"internal","namespace":"context-ns"},"spec":{"rules":[]}}`, `[{"name":"internal","namespace":"context-ns","default_backend_service":null,"labels":null,"annotations":null,"manifest":{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"internal","namespace":"context-ns"},"spec":{"rules":[]}}}]`, nil},
	} {
		t.Run(tt.query+tt.path, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != tt.path {
					t.Errorf("path = %s, want %s", r.URL.Path, tt.path)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"apiVersion":"v1","kind":"List","items":[%s]}`, tt.response)
			}))
			defer server.Close()
			args := append([]string{"--kubeconfig", configFile(t, server.URL), "--output", "json"}, tt.flags...)
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), args, strings.NewReader(tt.query), &stdout, &stderr)
			if code != 0 || stderr.Len() != 0 || calls != 1 {
				t.Fatalf("exit = %d, stderr = %s, calls = %d", code, &stderr, calls)
			}
			var got, want any
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("JSON = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestCLIErrorsBeforeAPI(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	path := configFile(t, server.URL)
	for _, tt := range []struct {
		query, code string
		flags       []string
	}{
		{"SELECT name, FROM deployments;", "E_PARSE", nil},
		{"SELECT name FROM ns;", "E_UNKNOWN_TABLE", nil},
		{"SELECT typo FROM deployments;", "E_UNKNOWN_COLUMN", nil},
		{"SELECT name FROM deployments WHERE typo IS NULL;", "E_UNKNOWN_COLUMN", nil},
		{"SELECT name FROM deployments WHERE replicas = '3';", "E_TYPE", nil},
		{"SELECT name FROM deployments WHERE TRUE OR replicas = '3';", "E_TYPE", nil},
		{"SELECT name FROM deployments WHERE name;", "E_TYPE", nil},
		{"SELECT name FROM deployments;", "E_FLAGS", []string{"--output", "yaml"}},
		{"SELECT name FROM deployments;", "E_FLAGS", []string{"extra"}},
	} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"--kubeconfig", path}, tt.flags...)
		code := Run(context.Background(), args, strings.NewReader(tt.query), &stdout, &stderr)
		var detail diagnostic
		if err := json.Unmarshal(stderr.Bytes(), &detail); err != nil || code != 2 || stdout.Len() != 0 || detail.Code != tt.code {
			t.Fatalf("exit = %d, stdout = %s, stderr = %s, error = %v", code, &stdout, &stderr, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid queries sent %d API requests", calls)
	}
	// Semantic errors take priority even over a nonexistent kubeconfig.
	var stdout, stderr bytes.Buffer
	Run(context.Background(), []string{"--kubeconfig", filepath.Join(t.TempDir(), "missing")}, strings.NewReader("SELECT typo FROM deployments"), &stdout, &stderr)
	if !strings.Contains(stderr.String(), "E_UNKNOWN_COLUMN") {
		t.Fatalf("error = %s", &stderr)
	}
}

func TestCLIForbiddenAndHelp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"Forbidden","message":"access denied","code":403}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader("SELECT name FROM namespaces"), &stdout, &stderr)
	var detail diagnostic
	if err := json.Unmarshal(stderr.Bytes(), &detail); err != nil || code != 1 || detail.Reason != "Forbidden" || stdout.Len() != 0 {
		t.Fatalf("exit = %d, error = %s, decode error = %v", code, &stderr, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"--help"}, strings.NewReader(""), &stdout, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "kubeconfig") {
		t.Fatalf("help exit = %d, stdout = %s, stderr = %s", code, &stdout, &stderr)
	}
}

func TestCLIWhereRuntimeErrorHasNoPartialOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"DeploymentList","items":[{"metadata":{"name":"valid"},"spec":{"replicas":3}},{"metadata":{"name":"invalid"},"spec":{"replicas":"sensitive-payload"}}]}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader("SELECT name FROM deployments WHERE replicas >= 2"), &stdout, &stderr)
	var detail diagnostic
	if err := json.Unmarshal(stderr.Bytes(), &detail); err != nil || code != 2 || detail.Code != "E_TYPE" || stdout.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s, decode error = %v", code, &stdout, &stderr, err)
	}
	if strings.Contains(stderr.String(), "sensitive-payload") {
		t.Fatal("diagnostic leaked resource value")
	}
}
