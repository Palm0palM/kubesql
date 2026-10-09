package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestPatchAndDeleteProtocol(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/apis/apps/v1/namespaces/chosen/deployments/web" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		switch r.Method {
		case http.MethodPatch:
			if r.Header.Get("Content-Type") != "application/json-patch+json" || string(data) != `[{"op":"test","path":"/metadata/resourceVersion","value":"12"}]` {
				t.Errorf("incorrect patch request: %s %s", r.Header.Get("Content-Type"), data)
			}
		case http.MethodDelete:
			var options metav1.DeleteOptions
			if err := json.Unmarshal(data, &options); err != nil || options.Preconditions == nil || options.Preconditions.UID == nil || options.Preconditions.ResourceVersion == nil {
				t.Errorf("missing delete preconditions: %s", data)
			} else if *options.Preconditions.UID != "identity" || *options.Preconditions.ResourceVersion != "12" || options.GracePeriodSeconds != nil {
				t.Errorf("unexpected delete options: %s", data)
			}
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"web","namespace":"chosen"}}`)
	}))
	defer server.Close()
	client, _, err := Connect(context.Background(), Options{Kubeconfig: writeConfig(t, server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	object := unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "web", "namespace": "chosen", "uid": "identity", "resourceVersion": "12"}}}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	if err := client.Patch(context.Background(), gvr, object, []byte(`[{"op":"test","path":"/metadata/resourceVersion","value":"12"}]`)); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(context.Background(), gvr, object); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
