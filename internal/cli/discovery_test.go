package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// serveDiscovery supplies a small real discovery protocol for CLI HTTP tests.
// Request counters in legacy tests count resource operations, not discovery.
func serveDiscovery(w http.ResponseWriter, r *http.Request) bool {
	var value any
	verbs := metav1.Verbs{"list", "create", "patch", "delete"}
	switch r.URL.Path {
	case "/api":
		value = metav1.APIVersions{Versions: []string{"v1"}}
	case "/apis":
		value = metav1.APIGroupList{Groups: []metav1.APIGroup{
			{Name: "apps", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "apps/v1", Version: "v1"}}, PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "apps/v1", Version: "v1"}},
			{Name: "networking.k8s.io", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "networking.k8s.io/v1", Version: "v1"}}, PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "networking.k8s.io/v1", Version: "v1"}},
		}}
	case "/api/v1":
		value = metav1.APIResourceList{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "namespaces", Kind: "Namespace", Verbs: verbs}, {Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: verbs}, {Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: verbs},
		}}
	case "/apis/apps/v1":
		value = metav1.APIResourceList{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{{Name: "deployments", Kind: "Deployment", Namespaced: true, Verbs: verbs}}}
	case "/apis/networking.k8s.io/v1":
		value = metav1.APIResourceList{GroupVersion: "networking.k8s.io/v1", APIResources: []metav1.APIResource{{Name: "ingresses", Kind: "Ingress", Namespaced: true, Verbs: verbs}}}
	default:
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
	return true
}

func TestGenericCLIUnknownTableAndWriteProtection(t *testing.T) {
	operations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveDiscovery(w, r) {
			return
		}
		operations++
	}))
	defer server.Close()
	for _, tt := range []struct{ input, code string }{
		{`SELECT name FROM cm`, "E_UNKNOWN_TABLE"},
		{`UPDATE configmaps SET "/metadata" = CAST('{}' AS JSON) WHERE TRUE`, "E_WRITE_PROTECTED"},
		{`UPDATE configmaps SET "/data/bad~2" = 'value' WHERE TRUE`, "E_POINTER"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader(tt.input), &stdout, &stderr)
		var detail diagnostic
		if err := json.Unmarshal(stderr.Bytes(), &detail); err != nil || code != 2 || detail.Code != tt.code || stdout.Len() != 0 || operations != 0 {
			t.Fatalf("exit=%d, stderr=%s, operations=%d", code, &stderr, operations)
		}
	}
}

func TestGenericCLIUnsupportedVerbBeforeList(t *testing.T) {
	operations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(metav1.APIResourceList{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"list"}}}})
			return
		}
		if serveDiscovery(w, r) {
			return
		}
		operations++
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--kubeconfig", configFile(t, server.URL)}, strings.NewReader(`UPDATE configmaps SET "/data/mode" = 'prod' WHERE TRUE`), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "E_UNSUPPORTED_VERB") || operations != 0 {
		t.Fatalf("exit=%d, stderr=%s, operations=%d", code, &stderr, operations)
	}
}
