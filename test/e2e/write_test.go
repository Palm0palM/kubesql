//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

func testClient(t *testing.T) dynamic.Interface {
	t.Helper()
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: os.Getenv("KSQL_E2E_KUBECONFIG")},
		&clientcmd.ConfigOverrides{CurrentContext: "kubesql-test"})
	restConfig, err := config.ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	restConfig.Timeout = 30 * time.Second
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func executeSQL(t *testing.T, ctx context.Context, binary string, args []string, input, want string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stderr.Len() != 0 {
		t.Fatalf("SQL failed: %v, stdout = %s, stderr = %s", err, &stdout, &stderr)
	}
	if strings.HasPrefix(want, "[") {
		// The Deployment controller independently adds its revision annotation.
		// Ignore only that known controller key in user-map assertions, never in CLI output.
		if strings.Contains(input, "FROM deployments") {
			var rows []map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if annotations, ok := row["annotations"].(map[string]any); ok {
					delete(annotations, "deployment.kubernetes.io/revision")
					if len(annotations) == 0 {
						row["annotations"] = nil
					}
				}
			}
			data, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			stdout.Reset()
			stdout.Write(data)
		}
		if got, expected := canonicalRows(t, stdout.Bytes()), canonicalRows(t, []byte(want)); !reflect.DeepEqual(got, expected) {
			t.Fatalf("rows = %v, want %v", got, expected)
		}
	} else {
		var got, expected map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(want), &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("result = %s, want %s", &stdout, want)
		}
	}
}

func TestMediumUpdate(t *testing.T) {
	ctx, binary, args := prepareFixture(t, "medium-write", "sql-medium-write")
	client := testClient(t)
	deployments := client.Resource(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}).Namespace("sql-medium-write")
	ingresses := client.Resource(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}).Namespace("sql-medium-write")
	beforeDeployment, err := deployments.Get(ctx, "web", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	beforeIngress, err := ingresses.Get(ctx, "temporary", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		"UPDATE deployments SET replicas = 2 WHERE name = 'web'",
		`UPDATE namespaces SET annotations = '{"owner":"alice"}' WHERE name = 'sql-medium-write'`,
		"UPDATE ingresses SET default_backend_service = 'web-v2' WHERE name = 'temporary'",
	} {
		executeSQL(t, ctx, binary, args, input, `{"affected_rows":1}`)
	}
	executeSQL(t, ctx, binary, args, "SELECT name, replicas, annotations FROM deployments WHERE name = 'web'", `[{"name":"web","replicas":2,"annotations":{"keep":"unchanged"}}]`)
	executeSQL(t, ctx, binary, args, "SELECT name, annotations FROM namespaces WHERE name = 'sql-medium-write'", `[{"name":"sql-medium-write","annotations":{"owner":"alice"}}]`)
	executeSQL(t, ctx, binary, args, "SELECT name, default_backend_service FROM ingresses WHERE name = 'temporary'", `[{"name":"temporary","default_backend_service":"web-v2"}]`)
	afterDeployment, err := deployments.Get(ctx, "web", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	afterIngress, err := ingresses.Get(ctx, "temporary", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	beforeDeployment.Object["spec"].(map[string]any)["replicas"] = int64(2)
	beforeIngress.Object["spec"].(map[string]any)["defaultBackend"].(map[string]any)["service"].(map[string]any)["name"] = "web-v2"
	if !reflect.DeepEqual(beforeDeployment.Object["spec"], afterDeployment.Object["spec"]) || !reflect.DeepEqual(beforeIngress.Object["spec"], afterIngress.Object["spec"]) {
		t.Fatal("UPDATE changed unspecified spec fields")
	}
	stalePatch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/resourceVersion", "value": beforeDeployment.GetResourceVersion()},
		{"op": "replace", "path": "/spec/replicas", "value": 99},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deployments.Patch(ctx, "web", types.JSONPatchType, stalePatch, metav1.PatchOptions{}); err == nil {
		t.Fatal("API accepted stale resourceVersion JSON Patch")
	}
	executeSQL(t, ctx, binary, args, "SELECT replicas FROM deployments WHERE name = 'web'", `[{"replicas":2}]`)
	// Prove that complete mappings are replaced, not merged, and multi-SET works.
	// Namespace avoids racing the Deployment controller restoring its revision key.
	executeSQL(t, ctx, binary, args, `UPDATE namespaces SET labels = '{"old":"remove"}', annotations = '{"old":"remove"}' WHERE name = 'sql-medium-write'`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `UPDATE namespaces SET labels = '{"new":"only"}', annotations = '{}' WHERE name = 'sql-medium-write'`, `{"affected_rows":1}`)
	// API Server restores this standard Namespace label; old user keys must still disappear.
	executeSQL(t, ctx, binary, args, "SELECT labels, annotations FROM namespaces WHERE name = 'sql-medium-write'", `[{"labels":{"new":"only","kubernetes.io/metadata.name":"sql-medium-write"},"annotations":null}]`)
	executeSQL(t, ctx, binary, args, "UPDATE deployments SET replicas = 0 WHERE name = 'absent'", `{"affected_rows":0}`)
}

func TestMediumDelete(t *testing.T) {
	ctx, binary, args := prepareFixture(t, "medium-write", "sql-medium-write")
	prepareFixture(t, "medium-delete", "sql-medium-delete")
	client := testClient(t)
	for _, tt := range []struct{ table, name, namespace, group, version string }{
		{"ingresses", "temporary", "sql-medium-write", "networking.k8s.io", "v1"},
		{"deployments", "web", "sql-medium-write", "apps", "v1"},
		{"namespaces", "sql-medium-delete", "", "", "v1"},
	} {
		resource := client.Resource(schema.GroupVersionResource{Group: tt.group, Version: tt.version, Resource: tt.table}).Namespace(tt.namespace)
		if tt.table == "ingresses" {
			if _, err := resource.Patch(ctx, tt.name, types.MergePatchType, []byte(`{"metadata":{"finalizers":["kubesql.test/hold"]}}`), metav1.PatchOptions{}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_, err := resource.Patch(cleanupCtx, tt.name, types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`), metav1.PatchOptions{})
				if err != nil && !apierrors.IsNotFound(err) {
					t.Errorf("remove test-owned finalizer: %v", err)
				}
			})
		}
		executeSQL(t, ctx, binary, args, "DELETE FROM "+tt.table+" WHERE name = '"+tt.name+"'", `{"affected_rows":1}`)
		if tt.table == "ingresses" {
			held, err := resource.Get(ctx, tt.name, metav1.GetOptions{})
			if err != nil || held.GetDeletionTimestamp() == nil || !reflect.DeepEqual(held.GetFinalizers(), []string{"kubesql.test/hold"}) {
				t.Fatalf("CLI must accept deletion without removing finalizers: object = %v, error = %v", held, err)
			}
			// Only the test removes the finalizer it created so cleanup can complete.
			if _, err := resource.Patch(ctx, tt.name, types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`), metav1.PatchOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		waitCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		err := wait.PollUntilContextCancel(waitCtx, time.Second, true, func(ctx context.Context) (bool, error) {
			_, err := resource.Get(ctx, tt.name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		executeSQL(t, ctx, binary, args, "SELECT name FROM "+tt.table+" WHERE name = '"+tt.name+"'", `[]`)
	}
}
