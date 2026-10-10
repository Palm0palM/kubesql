//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

func readFixture(t *testing.T, directory, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", directory, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// canonicalRows compares sets without converting JSON numbers to float64.
func canonicalRows(t *testing.T, data []byte) []string {
	t.Helper()
	var rows []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&rows); err != nil || rows == nil {
		t.Fatalf("expected JSON array, decode error: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("unexpected content after JSON array")
	}
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, string(encoded))
	}
	slices.Sort(result)
	return result
}

func prepareFixture(t *testing.T, directory, fixtureNamespace string) (context.Context, string, []string) {
	return prepareFixtureFile(t, directory, "fixture.yaml", fixtureNamespace)
}

func prepareFixtureFile(t *testing.T, directory, filename, fixtureNamespace string) (context.Context, string, []string) {
	t.Helper()
	path := os.Getenv("KSQL_E2E_KUBECONFIG")
	selectedContext := os.Getenv("KSQL_E2E_CONTEXT")
	binary := os.Getenv("KSQL_E2E_BINARY")
	if path == "" || selectedContext != "kubesql-test" || !filepath.IsAbs(binary) {
		t.Fatal("set KSQL_E2E_KUBECONFIG, KSQL_E2E_CONTEXT=kubesql-test and an absolute KSQL_E2E_BINARY; never uses the implicit current context")
	}
	rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: path}
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: selectedContext})
	restConfig, err := config.ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	restConfig.Timeout = 30 * time.Second
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	namespaces := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"})
	if _, err := namespaces.Get(ctx, fixtureNamespace, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("fixture namespace must not already exist (or API access failed): %v", err)
	}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(readFixture(t, directory, filename)), 4096)
	resources := map[string]schema.GroupVersionResource{
		"Namespace":             {Version: "v1", Resource: "namespaces"},
		"Deployment":            {Group: "apps", Version: "v1", Resource: "deployments"},
		"Service":               {Version: "v1", Resource: "services"},
		"Ingress":               {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		"ConfigMap":             {Version: "v1", Resource: "configmaps"},
		"Secret":                {Version: "v1", Resource: "secrets"},
		"ReplicaSet":            {Group: "apps", Version: "v1", Resource: "replicasets"},
		"StatefulSet":           {Group: "apps", Version: "v1", Resource: "statefulsets"},
		"PersistentVolumeClaim": {Version: "v1", Resource: "persistentvolumeclaims"},
		"Pod":                   {Version: "v1", Resource: "pods"},
	}
	for {
		var object unstructured.Unstructured
		if err := decoder.Decode(&object.Object); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		gvr, ok := resources[object.GetKind()]
		if !ok || (object.GetKind() == "Namespace" && object.GetName() != fixtureNamespace) ||
			(object.GetKind() != "Namespace" && object.GetNamespace() != fixtureNamespace) {
			t.Fatal("fixture has an unexpected resource or namespace")
		}
		if _, err := client.Resource(gvr).Namespace(object.GetNamespace()).Create(ctx, &object, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		if object.GetKind() == "Namespace" {
			registerNamespaceCleanup(t, client, fixtureNamespace)
		}
	}
	return ctx, binary, []string{"--kubeconfig", path, "--context", selectedContext, "--namespace", fixtureNamespace, "--output", "json"}
}

func registerNamespaceCleanup(t *testing.T, client dynamic.Interface, name string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		resource := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"})
		if err := resource.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleanup namespace: %v", err)
			return
		}
		if err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
			_, err := resource.Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}); err != nil {
			t.Errorf("wait for namespace cleanup: %v", err)
		}
	})
}

func TestEasySelect(t *testing.T) {
	ctx, binary, args := prepareFixture(t, "easy-select", "sql-easy-select")
	for _, name := range []string{"namespaces", "deployments", "ingresses"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Stdin = bytes.NewReader(readFixture(t, "easy-select", name+".sql"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("CLI failed: %v, stderr: %s", err, &stderr)
			}
			data := stdout.Bytes()
			if name == "ingresses" {
				// SELECT * now includes full API manifests. Verify those separately,
				// then compare the stable convenient columns with the chapter 2 fixture.
				var rows []map[string]any
				decoder := json.NewDecoder(bytes.NewReader(data))
				decoder.UseNumber()
				if err := decoder.Decode(&rows); err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					manifest, ok := row["manifest"].(map[string]any)
					if !ok || manifest["kind"] != "Ingress" || manifest["apiVersion"] != "networking.k8s.io/v1" {
						t.Fatal("SELECT * omitted full manifest")
					}
					metadata, ok := manifest["metadata"].(map[string]any)
					if !ok || metadata["name"] != row["name"] || metadata["namespace"] != row["namespace"] || metadata["resourceVersion"] == nil {
						t.Fatal("manifest metadata is incomplete")
					}
					delete(row, "manifest")
				}
				var err error
				data, err = json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
			}
			got := canonicalRows(t, data)
			if name == "namespaces" {
				if !slices.Contains(got, `{"name":"sql-easy-select"}`) || !slices.Contains(got, `{"name":"default"}`) {
					t.Fatalf("Namespace table was filtered by namespace: %v", got)
				}
			} else if want := canonicalRows(t, readFixture(t, "easy-select", name+".json")); !reflect.DeepEqual(got, want) {
				t.Fatalf("rows = %v, want %v", got, want)
			}
		})
	}
}
