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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCreateOnlyReviewCLI(t *testing.T) {
	for _, operation := range []struct {
		sql  string
		exit int
		want string
	}{
		{`INSERT INTO "authorization.k8s.io/v1/selfsubjectaccessreviews" (manifest) VALUES ('{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","spec":{"resourceAttributes":{"verb":"list","resource":"pods"}}}')`, 0, `{"affected_rows":1}`},
		{`SELECT name FROM "authorization.k8s.io/v1/selfsubjectaccessreviews"`, 2, "E_UNSUPPORTED_VERB"},
		{`UPDATE "authorization.k8s.io/v1/selfsubjectaccessreviews" SET "/spec/resourceAttributes/verb" = 'get' WHERE TRUE`, 2, "E_UNSUPPORTED_VERB"},
		{`DELETE FROM "authorization.k8s.io/v1/selfsubjectaccessreviews" WHERE TRUE`, 2, "E_UNSUPPORTED_VERB"},
	} {
		posts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet && r.URL.Path == "/apis/authorization.k8s.io/v1" {
				_ = json.NewEncoder(w).Encode(metav1.APIResourceList{GroupVersion: "authorization.k8s.io/v1", APIResources: []metav1.APIResource{{Name: "selfsubjectaccessreviews", Kind: "SelfSubjectAccessReview", Verbs: metav1.Verbs{"create"}}}})
				return
			}
			if r.Method != http.MethodPost || r.URL.Path != "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews" {
				t.Errorf("unexpected request=%s %s", r.Method, r.URL.Path)
			}
			posts++
			var object map[string]any
			if err := json.NewDecoder(r.Body).Decode(&object); err != nil {
				t.Error(err)
			}
			if _, exists := object["metadata"]; exists {
				t.Error("Review request invented metadata")
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":true}}`)
		}))
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL), "--namespace", "unrelated"}, strings.NewReader(operation.sql), &stdout, &stderr)
		server.Close()
		if code != operation.exit {
			t.Fatalf("exit=%d,stdout=%s,stderr=%s", code, &stdout, &stderr)
		}
		if operation.exit == 0 {
			if posts != 1 || strings.TrimSpace(stdout.String()) != operation.want || stderr.Len() != 0 {
				t.Fatalf("posts=%d,stdout=%s,stderr=%s", posts, &stdout, &stderr)
			}
		} else if posts != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), operation.want) {
			t.Fatalf("posts=%d,stdout=%s,stderr=%s", posts, &stdout, &stderr)
		}
	}
}

func TestPartialAggregatedAPICLI(t *testing.T) {
	operations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/apis" {
			_ = json.NewEncoder(w).Encode(metav1.APIGroupList{Groups: []metav1.APIGroup{{Name: "broken.example", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "broken.example/v1", Version: "v1"}}, PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "broken.example/v1", Version: "v1"}}}})
			return
		}
		if r.URL.Path == "/apis/broken.example/v1" {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"ServiceUnavailable","code":503,"message":"secret-discovery-body"}`)
			return
		}
		if serveDiscovery(w, r) {
			return
		}
		operations++
		fmt.Fprint(w, `{"kind":"ConfigMapList","apiVersion":"v1","items":[]}`)
	}))
	defer server.Close()
	for _, tt := range []struct {
		input      string
		exit       int
		operations int
	}{
		{`SELECT name FROM configmaps`, 1, 0},
		{`SELECT name FROM "v1/configmaps"`, 0, 1},
		{`SELECT name FROM "broken.example/v1/things"`, 1, 1},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader(tt.input), &stdout, &stderr)
		if code != tt.exit || operations != tt.operations || strings.Contains(stderr.String(), "secret-discovery-body") {
			t.Fatalf("exit=%d,operations=%d,stderr=%s", code, operations, &stderr)
		}
		if tt.exit == 1 && !strings.Contains(stderr.String(), "E_DISCOVERY") {
			t.Fatalf("lost discovery failure: %s", &stderr)
		}
	}
}

func TestCRWriteAPIErrorsContinue(t *testing.T) {
	for _, tt := range []struct {
		reason string
		status int
	}{{"Invalid", 422}, {"Forbidden", 403}, {"Conflict", 409}} {
		writes := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/apis/lab.example.com/v1" {
				_ = json.NewEncoder(w).Encode(metav1.APIResourceList{GroupVersion: "lab.example.com/v1", APIResources: []metav1.APIResource{{Name: "gadgets", Kind: "Widget", Namespaced: true, Verbs: metav1.Verbs{"list", "patch"}}}})
				return
			}
			if r.Method == http.MethodGet {
				fmt.Fprint(w, `{"apiVersion":"lab.example.com/v1","kind":"WidgetList","items":[{"metadata":{"name":"bad","namespace":"context-ns","resourceVersion":"1"},"spec":{"size":2}},{"metadata":{"name":"good","namespace":"context-ns","resourceVersion":"2"},"spec":{"size":2}}]}`)
				return
			}
			writes++
			if r.Method != http.MethodPatch {
				t.Errorf("unexpected method=%s", r.Method)
			}
			if strings.HasSuffix(r.URL.Path, "/bad") {
				w.WriteHeader(tt.status)
				fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Status","reason":%q,"code":%d,"message":"private-resource-value"}`, tt.reason, tt.status)
			} else {
				fmt.Fprint(w, `{"apiVersion":"lab.example.com/v1","kind":"Widget","metadata":{"name":"good"}}`)
			}
		}))
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader(`UPDATE "lab.example.com/v1/gadgets" SET "/spec/size" = 3 WHERE TRUE`), &stdout, &stderr)
		server.Close()
		var summary struct {
			AffectedRows int                                                  `json:"affected_rows"`
			FailedRows   int                                                  `json:"failed_rows"`
			Errors       []struct{ Resource, Namespace, Name, Reason string } `json:"errors"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil || code != 1 || writes != 2 || stderr.Len() != 0 || summary.AffectedRows != 1 || summary.FailedRows != 1 || len(summary.Errors) != 1 {
			t.Fatalf("exit=%d,writes=%d,stdout=%s,stderr=%s", code, writes, &stdout, &stderr)
		}
		if e := summary.Errors[0]; e.Reason != tt.reason || e.Resource != "gadgets" || e.Name != "bad" || e.Namespace != "context-ns" || strings.Contains(stdout.String(), "private-resource-value") {
			t.Fatalf("summary=%s", &stdout)
		}
	}
}
