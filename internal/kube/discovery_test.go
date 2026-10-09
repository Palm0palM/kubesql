package kube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Palm0palM/kubesql/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func discoveryList(gv, name, kind string, namespaced bool, verbs ...string) *metav1.APIResourceList {
	return &metav1.APIResourceList{GroupVersion: gv, APIResources: []metav1.APIResource{{Name: name, Kind: kind, Namespaced: namespaced, Verbs: verbs}}}
}

func discoveryGroup(name, preferred string, versions ...string) *metav1.APIGroup {
	g := &metav1.APIGroup{Name: name, PreferredVersion: metav1.GroupVersionForDiscovery{Version: preferred}}
	for _, version := range versions {
		g.Versions = append(g.Versions, metav1.GroupVersionForDiscovery{Version: version, GroupVersion: name + "/" + version})
	}
	return g
}

func TestSnapshotVersionScopeAndVerbs(t *testing.T) {
	s := Snapshot{Groups: []*metav1.APIGroup{discoveryGroup("apps", "v1", "v1", "v1beta2", "v1beta1")}, Lists: []*metav1.APIResourceList{
		discoveryList("apps/v1beta1", "statefulsets", "StatefulSet", true, "list"),
		discoveryList("apps/v1beta2", "statefulsets", "StatefulSet", true, "list", "patch"),
		discoveryList("apps/v1", "deployments", "Deployment", true, "list"),
		discoveryList("v1", "nodes", "Node", false, "list"),
	}}
	d, err := s.Resolve("statefulsets", false)
	if err != nil || d.GVR.Version != "v1beta2" || !d.Namespaced || len(d.Verbs) != 2 {
		t.Fatalf("descriptor=%+v, error=%v", d, err)
	}
	s.Lists = append(s.Lists, discoveryList("apps/v1", "statefulsets", "StatefulSet", true, "list"))
	d, err = s.Resolve("statefulsets", false)
	if err != nil || d.GVR.Version != "v1" {
		t.Fatalf("preferred descriptor=%+v, error=%v", d, err)
	}
	d, err = s.Resolve("apps/v1beta1/statefulsets", true)
	if err != nil || d.GVR.Version != "v1beta1" {
		t.Fatalf("explicit descriptor=%+v, error=%v", d, err)
	}
	if _, err := s.Resolve("apps/v2/statefulsets", true); err == nil {
		t.Fatal("explicit version fell back")
	}
	d, err = s.Resolve("v1/nodes", true)
	if err != nil || d.Namespaced {
		t.Fatal("wrong core cluster scope")
	}
	s.Lists = append(s.Lists, discoveryList("other/v1", "statefulsets", "Other", true, "list"))
	_, err = s.Resolve("statefulsets", false)
	var detail *resource.Error
	if !errors.As(err, &detail) || detail.Code != "E_AMBIGUOUS_TABLE" {
		t.Fatalf("ambiguity error=%v", err)
	}
}

func TestPartialDiscoveryAndSubresources(t *testing.T) {
	s := Snapshot{Lists: []*metav1.APIResourceList{discoveryList("v1", "configmaps", "ConfigMap", true, "list"), discoveryList("apps/v1", "deployments/status", "Deployment", true, "get")}, Failed: map[schema.GroupVersion]error{{Group: "broken.example", Version: "v1"}: errors.New("sensitive remote body")}}
	if _, err := s.Resolve("v1/configmaps", true); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		quoted bool
		code   string
	}{
		{"configmaps", false, "E_DISCOVERY"}, {"broken.example/v1/things", true, "E_DISCOVERY"},
		{"apps/v1/deployments/status", true, "E_TABLE"}, {"deployments/status", false, "E_TABLE"},
		{"v1/cm", true, "E_UNKNOWN_TABLE"},
	} {
		_, err := s.Resolve(tt.name, tt.quoted)
		var detail *resource.Error
		if !errors.As(err, &detail) || detail.Code != tt.code {
			t.Fatalf("%s: error=%v", tt.name, err)
		}
	}
}

func TestExactDiscoveryUsesOnlyRequestedVersion(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1" {
			t.Errorf("unexpected discovery path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(discoveryList("v1", "configmaps", "ConfigMap", true, "list"))
	}))
	defer server.Close()
	c, _, err := Connect(context.Background(), Options{Kubeconfig: writeConfig(t, server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Resolve(context.Background(), "v1/configmaps", true)
	if err != nil || calls != 1 || d.Kind != "ConfigMap" {
		t.Fatalf("descriptor=%+v, calls=%d, error=%v", d, calls, err)
	}
}

func TestDiscoveryCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _, err := Connect(ctx, Options{Kubeconfig: writeConfig(t, server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := c.Resolve(ctx, "v1/configmaps", true); finished <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled discovery succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("discovery ignored cancellation")
	}
}

func TestPartialDiscoveryHTTPKeepsExactHealthyResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			_ = json.NewEncoder(w).Encode(metav1.APIVersions{Versions: []string{"v1"}})
		case "/apis":
			_ = json.NewEncoder(w).Encode(metav1.APIGroupList{Groups: []metav1.APIGroup{*discoveryGroup("broken.example", "v1", "v1")}})
		case "/api/v1":
			_ = json.NewEncoder(w).Encode(discoveryList("v1", "configmaps", "ConfigMap", true, "list"))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(metav1.Status{Status: "Failure", Reason: metav1.StatusReasonServiceUnavailable, Code: 503, Message: "sensitive-remote-body"})
		}
	}))
	defer server.Close()
	c, _, err := Connect(context.Background(), Options{Kubeconfig: writeConfig(t, server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Resolve(context.Background(), "configmaps", false)
	var detail *resource.Error
	if !errors.As(err, &detail) || detail.Code != "E_DISCOVERY" {
		t.Fatalf("partial discovery error=%v", err)
	}
	if d, err := c.Resolve(context.Background(), "v1/configmaps", true); err != nil || d.Kind != "ConfigMap" {
		t.Fatalf("healthy exact descriptor=%+v, error=%v", d, err)
	}
}
