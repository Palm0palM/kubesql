//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	jsonutil "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

func fixtureObject(t *testing.T, directory, file string) unstructured.Unstructured {
	t.Helper()
	var object unstructured.Unstructured
	var raw json.RawMessage
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(readFixture(t, directory, file)), 4096).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if err := jsonutil.Unmarshal(raw, &object.Object); err != nil {
		t.Fatal(err)
	}
	return object
}

func manifestSQL(t *testing.T, table string, object unstructured.Unstructured) string {
	t.Helper()
	data, err := json.Marshal(object.Object)
	if err != nil {
		t.Fatal(err)
	}
	return `INSERT INTO "` + table + `" (manifest) VALUES ('` + strings.ReplaceAll(string(data), "'", "''") + `')`
}

func waitAbsent(t *testing.T, ctx context.Context, r dynamic.ResourceInterface, name string) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if err := wait.PollUntilContextCancel(waitCtx, time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := r.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}); err != nil {
		t.Fatalf("wait deletion of %s: %v", name, err)
	}
}

// createCRD refuses existing definitions, creates with the real SQL binary, then
// waits for Established and exact Discovery independently before any CR request.
func createCRD(t *testing.T, ctx context.Context, binary string, args []string, directory, file string) unstructured.Unstructured {
	t.Helper()
	target := fixtureObject(t, directory, file)
	client := testClient(t)
	definitions := client.Resource(crdGVR)
	if _, err := definitions.Get(ctx, target.GetName(), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("test CRD must not already exist: %v", err)
	}
	executeSQL(t, ctx, binary, args, manifestSQL(t, "apiextensions.k8s.io/v1/customresourcedefinitions", target), `{"affected_rows":1}`)
	created, err := definitions.Get(ctx, target.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	uid := created.GetUID()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		err := definitions.Delete(cleanupCtx, target.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleanup test-owned CRD: %v", err)
			return
		}
		waitAbsent(t, cleanupCtx, definitions, target.GetName())
	})
	assertSubset(t, "CRD target", target.Object, created.Object)
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := wait.PollUntilContextCancel(waitCtx, time.Second, true, func(ctx context.Context) (bool, error) {
		object, err := definitions.Get(ctx, target.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		// Fresh CRDs may expose conditions:null before the registration controller
		// writes its first condition; that is not an Established failure.
		raw, _, err := unstructured.NestedFieldNoCopy(object.Object, "status", "conditions")
		if err != nil || raw == nil {
			return false, err
		}
		conditions, _, err := unstructured.NestedSlice(object.Object, "status", "conditions")
		if err != nil {
			return false, err
		}
		for _, raw := range conditions {
			condition, ok := raw.(map[string]any)
			if ok && condition["type"] == "Established" && condition["status"] == "True" {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatalf("wait CRD Established: %v", err)
	}
	group, _, _ := unstructured.NestedString(target.Object, "spec", "group")
	plural, _, _ := unstructured.NestedString(target.Object, "spec", "names", "plural")
	waitCRDiscovery(t, waitCtx, binary, args, group+"/v1/"+plural)
	return target
}

func waitCRDiscovery(t *testing.T, ctx context.Context, binary string, args []string, table string) {
	t.Helper()
	if err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = strings.NewReader(`SELECT name FROM "` + table + `"`)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err == nil {
			return stderr.Len() == 0, nil
		}
		var detail struct{ Code string }
		_ = json.Unmarshal(stderr.Bytes(), &detail)
		if detail.Code == "E_DISCOVERY" || detail.Code == "E_UNKNOWN_TABLE" {
			return false, nil
		}
		return false, err
	}); err != nil {
		t.Fatalf("wait Discovery %s: %v", table, err)
	}
}

func expectWriteFailure(t *testing.T, ctx context.Context, binary string, args []string, input, resource, name, reason string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || stderr.Len() != 0 {
		t.Fatalf("expected API failure, exit=%v,stdout=%s,stderr=%s", err, &stdout, &stderr)
	}
	var result struct {
		AffectedRows int                                                  `json:"affected_rows"`
		FailedRows   int                                                  `json:"failed_rows"`
		Errors       []struct{ Resource, Namespace, Name, Reason string } `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.AffectedRows != 0 || result.FailedRows != 1 || len(result.Errors) != 1 {
		t.Fatalf("invalid failure summary: %s", &stdout)
	}
	if e := result.Errors[0]; e.Resource != resource || e.Name != name || e.Reason != reason {
		t.Fatalf("wrong failure identity: %s", &stdout)
	}
	wantNamespace := ""
	if resource == "gadgets" {
		wantNamespace = "sql-hard-crd"
	}
	if result.Errors[0].Namespace != wantNamespace {
		t.Fatalf("wrong failure namespace: %s", &stdout)
	}
}

func TestHardCRDNamespaced(t *testing.T) {
	ctx, binary, args := prepareFixtureFile(t, "hard-crd", "preparation.yaml", "sql-hard-crd")
	createCRD(t, ctx, binary, args, "hard-crd", "gadgets-crd.yaml")
	client := testClient(t)
	resource := client.Resource(schema.GroupVersionResource{Group: "lab.example.com", Version: "v1", Resource: "gadgets"}).Namespace("sql-hard-crd")
	sample := fixtureObject(t, "hard-crd", "sample.yaml")
	if _, err := resource.Create(ctx, &sample, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	executeSQL(t, ctx, binary, args, string(readFixture(t, "hard-crd", "select.sql")), string(readFixture(t, "hard-crd", "select.json")))
	executeSQL(t, ctx, binary, args, string(readFixture(t, "hard-crd", "update.sql")), `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT name, "/spec/size" AS size, "/spec/message" AS message FROM gadgets WHERE name = 'sample'`, `[{"name":"sample","size":3,"message":"hello"}]`)
	if _, err := resource.Get(ctx, "extra", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("INSERT target existed: %v", err)
	}
	executeSQL(t, ctx, binary, args, string(readFixture(t, "hard-crd", "insert.sql")), `{"affected_rows":1}`)
	extra, err := resource.Get(ctx, "extra", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertSubset(t, "extra target", fixtureObject(t, "hard-crd", "extra-target.yaml").Object, extra.Object)
	executeSQL(t, ctx, binary, args, string(readFixture(t, "hard-crd", "delete.sql")), `{"affected_rows":1}`)
	waitAbsent(t, ctx, resource, "extra")
	executeSQL(t, ctx, binary, args, `SELECT name, "/spec/size" AS size FROM "lab.example.com/v1/gadgets"`, `[{"name":"sample","size":3}]`)
	// API schema, not SQL coercion, rejects invalid values; existing object is untouched.
	before, err := resource.Get(ctx, "sample", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expectWriteFailure(t, ctx, binary, args, `UPDATE gadgets SET "/spec/size" = 0 WHERE name = 'sample'`, "gadgets", "sample", "Invalid")
	after, err := resource.Get(ctx, "sample", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Object, after.Object) {
		t.Fatal("schema rejection modified object")
	}
	expectWriteFailure(t, ctx, binary, args, `INSERT INTO gadgets (manifest) VALUES ('{"apiVersion":"lab.example.com/v1","kind":"Widget","metadata":{"name":"invalid"},"spec":{"size":0}}')`, "gadgets", "invalid", "Invalid")
	if _, err := resource.Get(ctx, "invalid", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("invalid CR persisted: %v", err)
	}
	// Add a second served version by SQL, preserving a single storage version.
	definitions := client.Resource(crdGVR)
	crd, err := definitions.Get(ctx, "gadgets.lab.example.com", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	beta := crd.DeepCopy().Object["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)
	beta["name"], beta["storage"] = "v1beta1", false
	versions = append(versions, beta)
	data, err := json.Marshal(versions)
	if err != nil {
		t.Fatal(err)
	}
	executeSQL(t, ctx, binary, args, `UPDATE "apiextensions.k8s.io/v1/customresourcedefinitions" SET "/spec/versions" = CAST('`+strings.ReplaceAll(string(data), "'", "''")+`' AS JSON) WHERE name = 'gadgets.lab.example.com'`, `{"affected_rows":1}`)
	waitCRDiscovery(t, ctx, binary, args, "lab.example.com/v1beta1/gadgets")
	executeSQL(t, ctx, binary, args, `SELECT name, "/apiVersion" AS version FROM gadgets WHERE name = 'sample'`, `[{"name":"sample","version":"lab.example.com/v1"}]`)
	executeSQL(t, ctx, binary, args, `SELECT name, "/apiVersion" AS version FROM "lab.example.com/v1beta1/gadgets" WHERE name = 'sample'`, `[{"name":"sample","version":"lab.example.com/v1beta1"}]`)
	executeSQL(t, ctx, binary, args, `INSERT INTO "lab.example.com/v1beta1/gadgets" (manifest) VALUES ('{"apiVersion":"lab.example.com/v1beta1","kind":"Widget","metadata":{"name":"beta"},"spec":{"size":4}}')`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT name, "/spec/size" AS size FROM gadgets WHERE name = 'beta'`, `[{"name":"beta","size":4}]`)
	expectWriteFailure(t, ctx, binary, args, `UPDATE "apiextensions.k8s.io/v1/customresourcedefinitions" SET "/spec/scope" = 'Cluster' WHERE name = 'gadgets.lab.example.com'`, "customresourcedefinitions", "gadgets.lab.example.com", "Invalid")
	// A stale optimistic patch is rejected by the real API, not just a fake reactor.
	stale, _ := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/resourceVersion", "value": before.GetResourceVersion()}, {"op": "replace", "path": "/spec/size", "value": 9}})
	executeSQL(t, ctx, binary, args, `UPDATE gadgets SET "/spec/size" = 4 WHERE name = 'sample'`, `{"affected_rows":1}`)
	if _, err := resource.Patch(ctx, "sample", types.JSONPatchType, stale, metav1.PatchOptions{}); err == nil {
		t.Fatal("stale JSON Patch accepted")
	}
	// Impersonate a test-owned ServiceAccount with no role grants. Discovery is
	// available to authenticated users; the resource request must return Forbidden.
	account := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": "restricted", "namespace": "sql-hard-crd"}}}
	if _, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}).Namespace("sql-hard-crd").Create(ctx, &account, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	config, err := clientcmd.LoadFromFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	selected := config.Contexts["kubesql-test"]
	user := config.AuthInfos[selected.AuthInfo]
	user.Impersonate = "system:serviceaccount:sql-hard-crd:restricted"
	user.ImpersonateGroups = []string{"system:serviceaccounts", "system:serviceaccounts:sql-hard-crd", "system:authenticated"}
	path := filepath.Join(t.TempDir(), "restricted-kubeconfig")
	encoded, err := clientcmd.Write(*config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	restrictedArgs := append([]string{}, args...)
	restrictedArgs[1] = path
	expectWriteFailure(t, ctx, binary, restrictedArgs, `INSERT INTO "lab.example.com/v1/gadgets" (manifest) VALUES ('{"apiVersion":"lab.example.com/v1","kind":"Widget","metadata":{"name":"forbidden"},"spec":{"size":2}}')`, "gadgets", "forbidden", "Forbidden")
	if _, err := resource.Get(ctx, "forbidden", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Forbidden CR persisted: %v", err)
	}
	executeSQL(t, ctx, binary, args, `SELECT "/spec/size" AS size, "/spec/message" AS message FROM gadgets WHERE name = 'sample'`, `[{"size":4,"message":"hello"}]`)
}

func TestHardCRDCluster(t *testing.T) {
	ctx, binary, args := prepareFixtureFile(t, "hard-cluster-crd", "preparation.yaml", "sql-hard-cluster-crd")
	createCRD(t, ctx, binary, args, "hard-cluster-crd", "clusternotes-crd.yaml")
	client := testClient(t)
	notes := client.Resource(schema.GroupVersionResource{Group: "lab.example.com", Version: "v1", Resource: "clusternotes"})
	sample := fixtureObject(t, "hard-cluster-crd", "sample.yaml")
	executeSQL(t, ctx, binary, args, manifestSQL(t, "lab.example.com/v1/clusternotes", sample), `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, string(readFixture(t, "hard-cluster-crd", "select.sql")), string(readFixture(t, "hard-cluster-crd", "select.json")))
	executeSQL(t, ctx, binary, args, string(readFixture(t, "hard-cluster-crd", "select-crd.sql")), `[{"name":"clusternotes.lab.example.com"}]`)
	executeSQL(t, ctx, binary, args, `UPDATE clusternotes SET "/spec/owner" = 'updated' WHERE name = 'cluster-note'`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT name, namespace, "/spec/owner" AS owner FROM clusternotes`, `[{"name":"cluster-note","namespace":null,"owner":"updated"}]`)
	executeSQL(t, ctx, binary, args, `UPDATE "apiextensions.k8s.io/v1/customresourcedefinitions" SET annotations = '{"test.example.com/owner":"ksql"}' WHERE name = 'clusternotes.lab.example.com'`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT name, namespace, annotations FROM "apiextensions.k8s.io/v1/customresourcedefinitions" WHERE name = 'clusternotes.lab.example.com'`, `[{"name":"clusternotes.lab.example.com","namespace":null,"annotations":{"test.example.com/owner":"ksql"}}]`)
	executeSQL(t, ctx, binary, args, `DELETE FROM clusternotes WHERE name = 'cluster-note'`, `{"affected_rows":1}`)
	waitAbsent(t, ctx, notes, "cluster-note")
	executeSQL(t, ctx, binary, args, `DELETE FROM "apiextensions.k8s.io/v1/customresourcedefinitions" WHERE name = 'clusternotes.lab.example.com'`, `{"affected_rows":1}`)
	waitAbsent(t, ctx, client.Resource(crdGVR), "clusternotes.lab.example.com")
	// Real create-only Review accepts a request without metadata.name.
	executeSQL(t, ctx, binary, args, `INSERT INTO "authorization.k8s.io/v1/selfsubjectaccessreviews" (manifest) VALUES ('{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","spec":{"resourceAttributes":{"verb":"list","resource":"pods"}}}')`, `{"affected_rows":1}`)
}
