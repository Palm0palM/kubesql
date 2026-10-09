//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"reflect"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	jsonutil "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// assertSubset checks only fields explicitly supplied in target manifests.
func assertSubset(t *testing.T, path string, want, got any) {
	t.Helper()
	switch expected := want.(type) {
	case map[string]any:
		actual, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("%s: expected object, got %T", path, got)
		}
		for key, value := range expected {
			assertSubset(t, path+"/"+key, value, actual[key])
		}
	case []any:
		actual, ok := got.([]any)
		if !ok || len(actual) != len(expected) {
			t.Fatalf("%s: array differs", path)
		}
		for index, value := range expected {
			assertSubset(t, path, value, actual[index])
		}
	default:
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s: got %v, want %v", path, got, want)
		}
	}
}

func assertCreatedTargets(t *testing.T, ctx context.Context, directory string) {
	t.Helper()
	client := testClient(t)
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(readFixture(t, directory, "targets.yaml")), 4096)
	resources := map[string]schema.GroupVersionResource{
		"Namespace":  {Version: "v1", Resource: "namespaces"},
		"Deployment": {Group: "apps", Version: "v1", Resource: "deployments"},
		"Ingress":    {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	}
	for {
		var target unstructured.Unstructured
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if err := jsonutil.Unmarshal(raw, &target.Object); err != nil {
			t.Fatal(err)
		}
		gvr, ok := resources[target.GetKind()]
		if !ok {
			t.Fatal("unexpected target kind")
		}
		object, err := client.Resource(gvr).Namespace(target.GetNamespace()).Get(ctx, target.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		assertSubset(t, target.GetName(), target.Object, object.Object)
	}
}

func TestMediumInsertNamespaceDeployment(t *testing.T) {
	ctx, binary, args := prepareFixtureFile(t, "medium-insert", "preparation.yaml", "sql-medium-insert")
	client := testClient(t)
	// Empty preparation: Namespace must be created by SQL, not by fixtures.
	registerNamespaceCleanup(t, client, "sql-medium-insert")
	executeSQL(t, ctx, binary, args, string(readFixture(t, "medium-insert", "namespace.sql")), `{"affected_rows":1}`)
	namespaces := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"})
	if err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		object, err := namespaces.Get(ctx, "sql-medium-insert", metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		phase, _, err := unstructured.NestedString(object.Object, "status", "phase")
		return phase == "Active", err
	}); err != nil {
		t.Fatal(err)
	}
	deployments := client.Resource(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}).Namespace("sql-medium-insert")
	if _, err := deployments.Get(ctx, "api", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("target must not exist: %v", err)
	}
	executeSQL(t, ctx, binary, args, string(readFixture(t, "medium-insert", "deployment.sql")), `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, "SELECT name, namespace, replicas FROM deployments WHERE name = 'api'", `[{"name":"api","namespace":"sql-medium-insert","replicas":2}]`)
	assertCreatedTargets(t, ctx, "medium-insert")
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = bytes.NewBufferString("SELECT manifest FROM deployments WHERE name = 'api'")
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	// Match the dynamic client's Kubernetes number representation.
	if err := jsonutil.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatal("expected one manifest row")
	}
	object, err := deployments.Get(ctx, "api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Controller may update status between List/Get; compare target fields and identity.
	manifest, ok := rows[0]["manifest"].(map[string]any)
	if !ok {
		t.Fatal("manifest must be an object, not a JSON string")
	}
	assertSubset(t, "manifest", map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "spec": object.Object["spec"]}, manifest)
	metadata := manifest["metadata"].(map[string]any)
	if metadata["uid"] != string(object.GetUID()) || metadata["resourceVersion"] == nil {
		t.Fatal("manifest omitted server metadata")
	}
	// Missing Deployment spec must be rejected by API schema, not silently filled.
	invalid := exec.CommandContext(ctx, binary, args...)
	invalid.Stdin = bytes.NewBufferString(`INSERT INTO deployments (manifest) VALUES ('{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"invalid"}}')`)
	var stdout, stderr bytes.Buffer
	invalid.Stdout, invalid.Stderr = &stdout, &stderr
	if err := invalid.Run(); err == nil {
		t.Fatal("API accepted a Deployment without selector/template")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || stderr.Len() != 0 {
		t.Fatalf("schema rejection exit=%v, stderr=%s", err, &stderr)
	}
	var summary struct {
		AffectedRows int                       `json:"affected_rows"`
		FailedRows   int                       `json:"failed_rows"`
		Errors       []struct{ Reason string } `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil || summary.AffectedRows != 0 || summary.FailedRows != 1 || len(summary.Errors) != 1 || summary.Errors[0].Reason != "Invalid" {
		t.Fatalf("schema rejection summary=%s, error=%v", &stdout, err)
	}
	if _, err := deployments.Get(ctx, "invalid", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("invalid Deployment persisted: %v", err)
	}
}

func TestMediumInsertIngressAlreadyExists(t *testing.T) {
	ctx, binary, args := prepareFixtureFile(t, "medium-insert-ingress", "preparation.yaml", "sql-medium-insert-ingress")
	resource := testClient(t).Resource(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}).Namespace("sql-medium-insert-ingress")
	if _, err := resource.Get(ctx, "web", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("target must not exist: %v", err)
	}
	input := readFixture(t, "medium-insert-ingress", "ingress.sql")
	executeSQL(t, ctx, binary, args, string(input), `{"affected_rows":1}`)
	assertCreatedTargets(t, ctx, "medium-insert-ingress")
	before, err := resource.Get(ctx, "web", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || stderr.Len() != 0 {
		t.Fatalf("duplicate exit=%v, stderr=%s", err, &stderr)
	}
	var result any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var expected any
	_ = json.Unmarshal([]byte(`{"affected_rows":0,"failed_rows":1,"errors":[{"resource":"ingresses","namespace":"sql-medium-insert-ingress","name":"web","reason":"AlreadyExists"}]}`), &expected)
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("duplicate summary=%s", &stdout)
	}
	after, err := resource.Get(ctx, "web", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Object, after.Object) {
		t.Fatal("duplicate create changed original object")
	}
	list, err := resource.List(ctx, metav1.ListOptions{})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("expected only one Ingress: %v", err)
	}
	// Omitted namespace uses CLI scope; SQL '' and JSON escapes remain independent.
	executeSQL(t, ctx, binary, args, `INSERT INTO ingresses (manifest) VALUES ('{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"quoted","annotations":{"owner":"alice''s","path":"a\\b"}},"spec":{"defaultBackend":{"service":{"name":"web","port":{"number":80}}}}}')`, `{"affected_rows":1}`)
	executeSQL(t, ctx, binary, args, `SELECT name, namespace, annotations FROM ingresses WHERE name = 'quoted'`, `[{"name":"quoted","namespace":"sql-medium-insert-ingress","annotations":{"owner":"alice's","path":"a\\b"}}]`)
}
