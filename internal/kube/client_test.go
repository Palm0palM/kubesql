package kube

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func writeConfig(t *testing.T, server string) string {
	t.Helper()
	config := clientcmdapi.NewConfig()
	config.Clusters["test"] = &clientcmdapi.Cluster{Server: server}
	config.AuthInfos["test"] = &clientcmdapi.AuthInfo{}
	config.Contexts["current"] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test", Namespace: "context-ns"}
	config.Contexts["other"] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test"}
	config.CurrentContext = "current"
	path := filepath.Join(t.TempDir(), "config")
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigNamespaceAndContext(t *testing.T) {
	path := writeConfig(t, "http://127.0.0.1:1")
	for _, tt := range []struct{ context, namespace, want string }{
		{"", "", "context-ns"}, {"other", "", "default"},
		{"other", "explicit", "explicit"}, {"", "explicit", "explicit"},
	} {
		_, got, err := Connect(Options{Kubeconfig: path, Context: tt.context, Namespace: tt.namespace})
		if err != nil || got != tt.want {
			t.Fatalf("options = %+v, namespace = %q, error = %v", tt, got, err)
		}
	}
	t.Setenv("KUBECONFIG", path)
	if _, got, err := Connect(Options{}); err != nil || got != "context-ns" {
		t.Fatalf("KUBECONFIG: namespace = %q, error = %v", got, err)
	}
	if _, _, err := Connect(Options{Kubeconfig: path, Context: "missing"}); err == nil {
		t.Fatal("missing context must fail")
	}
	if _, _, err := Connect(Options{Kubeconfig: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing explicit kubeconfig must fail")
	}
}

func TestDynamicListPaginationAndIntegerPrecision(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/apis/apps/v1/namespaces/chosen/deployments" || r.URL.Query().Get("limit") != "500" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"DeploymentList","metadata":{"continue":"page2"},"items":[{"metadata":{"name":"web"},"spec":{"replicas":9007199254740993}}]}`)
		} else {
			if r.URL.Query().Get("continue") != "page2" {
				t.Errorf("missing continue token: %s", r.URL)
			}
			fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"DeploymentList","items":[{"metadata":{"name":"worker"}}]}`)
		}
	}))
	defer server.Close()
	client, _, err := Connect(Options{Kubeconfig: writeConfig(t, server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := client.List(context.Background(), schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, "chosen")
	if err != nil || calls != 2 || len(objects) != 2 {
		t.Fatalf("objects = %v, calls = %d, error = %v", objects, calls, err)
	}
	if got := objects[0].Object["spec"].(map[string]any)["replicas"]; got != int64(9007199254740993) {
		t.Fatalf("integer precision lost: %v (%T)", got, got)
	}
}

func TestListPreservesForbiddenAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","message":"access denied","code":403}`)
	}))
	defer server.Close()
	client, _, err := Connect(Options{Kubeconfig: writeConfig(t, server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	if _, err := client.List(context.Background(), gvr, ""); !apierrors.IsForbidden(err) {
		t.Fatalf("error = %v, want Forbidden", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.List(ctx, gvr, ""); err == nil {
		t.Fatal("cancelled list must fail")
	}
}
