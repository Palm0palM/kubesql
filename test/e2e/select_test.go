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

const fixtureNamespace = "sql-easy-select"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "easy-select", name))
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

func TestEasySelect(t *testing.T) {
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
	defer cancel()
	namespaces := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"})
	if _, err := namespaces.Get(ctx, fixtureNamespace, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("fixture namespace must not already exist (or API access failed): %v", err)
	}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(readFixture(t, "fixture.yaml")), 4096)
	resources := map[string]schema.GroupVersionResource{
		"Namespace":  {Version: "v1", Resource: "namespaces"},
		"Deployment": {Group: "apps", Version: "v1", Resource: "deployments"},
		"Service":    {Version: "v1", Resource: "services"},
		"Ingress":    {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
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
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				if err := namespaces.Delete(cleanupCtx, fixtureNamespace, metav1.DeleteOptions{}); err != nil {
					t.Errorf("cleanup namespace: %v", err)
					return
				}
				err := wait.PollUntilContextCancel(cleanupCtx, time.Second, true, func(ctx context.Context) (bool, error) {
					_, err := namespaces.Get(ctx, fixtureNamespace, metav1.GetOptions{})
					if apierrors.IsNotFound(err) {
						return true, nil
					}
					return false, err
				})
				if err != nil {
					t.Errorf("wait for namespace cleanup: %v", err)
				}
			})
		}
	}
	for _, name := range []string{"namespaces", "deployments", "ingresses"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, binary, "--kubeconfig", path, "--context", selectedContext, "--namespace", fixtureNamespace, "--output", "json")
			cmd.Stdin = bytes.NewReader(readFixture(t, name+".sql"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("CLI failed: %v, stderr: %s", err, &stderr)
			}
			got := canonicalRows(t, stdout.Bytes())
			if name == "namespaces" {
				if !slices.Contains(got, `{"name":"sql-easy-select"}`) || !slices.Contains(got, `{"name":"default"}`) {
					t.Fatalf("Namespace table was filtered by namespace: %v", got)
				}
			} else if want := canonicalRows(t, readFixture(t, name+".json")); !reflect.DeepEqual(got, want) {
				t.Fatalf("rows = %v, want %v", got, want)
			}
		})
	}
}
