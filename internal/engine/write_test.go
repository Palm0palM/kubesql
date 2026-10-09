package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/Palm0palM/kubesql/internal/sql"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type stubWriter struct {
	stubLister
	patches map[string][]byte
	deleted []string
	fail    string
}

func (s *stubWriter) Patch(_ context.Context, gvr schema.GroupVersionResource, object unstructured.Unstructured, data []byte) error {
	if s.patches == nil {
		s.patches = make(map[string][]byte)
	}
	s.patches[object.GetName()] = data
	if object.GetName() == s.fail {
		return apierrors.NewConflict(gvr.GroupResource(), object.GetName(), errors.New("sensitive contents"))
	}
	return nil
}

func (s *stubWriter) Delete(_ context.Context, gvr schema.GroupVersionResource, object unstructured.Unstructured) error {
	s.deleted = append(s.deleted, object.GetName())
	if object.GetName() == s.fail {
		return apierrors.NewForbidden(gvr.GroupResource(), object.GetName(), errors.New("sensitive contents"))
	}
	return nil
}

func bindWrite(t *testing.T, input string) *Write {
	t.Helper()
	stmt, err := sql.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	w, err := BindWrite(stmt, false)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func writeObject(name string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": name, "namespace": "test", "resourceVersion": "12", "uid": "stable-uid", "labels": map[string]any{"old": "remove"}},
		"spec":     map[string]any{"replicas": int64(0)},
	}}
}

func TestWriteValidation(t *testing.T) {
	for input, code := range map[string]string{
		"UPDATE deployments SET replicas = 2": "E_WHERE_REQUIRED", "DELETE FROM namespaces": "E_WHERE_REQUIRED",
		"UPDATE deployments SET replicas = -1 WHERE TRUE":                      "E_TYPE",
		"UPDATE deployments SET replicas = 1.5 WHERE TRUE":                     "E_TYPE",
		"UPDATE deployments SET replicas = 2147483648 WHERE TRUE":              "E_TYPE",
		"UPDATE deployments SET replicas = NULL WHERE TRUE":                    "E_TYPE",
		"UPDATE deployments SET replicas = name WHERE TRUE":                    "E_TYPE",
		"UPDATE deployments SET name = 'x' WHERE TRUE":                         "E_WRITE_COLUMN",
		"UPDATE deployments SET namespace = 'x' WHERE TRUE":                    "E_WRITE_COLUMN",
		"UPDATE deployments SET status = 'x' WHERE TRUE":                       "E_WRITE_COLUMN",
		"UPDATE deployments SET replicas = 1, replicas = 2 WHERE TRUE":         "E_DUPLICATE_COLUMN",
		`UPDATE deployments SET labels = '{"key":null}' WHERE TRUE`:            "E_TYPE",
		`UPDATE namespaces SET annotations = '[]' WHERE TRUE`:                  "E_TYPE",
		`UPDATE namespaces SET labels = 'null' WHERE TRUE`:                     "E_TYPE",
		`UPDATE namespaces SET labels = '{}' WHERE typo IS NULL`:               "E_UNKNOWN_COLUMN",
		`UPDATE ingresses SET default_backend_service = '' WHERE TRUE`:         "E_TYPE",
		`UPDATE ingresses SET default_backend_service = 'Bad_Name' WHERE TRUE`: "E_TYPE",
		`UPDATE ingresses SET default_backend_service = NULL WHERE TRUE`:       "E_TYPE",
	} {
		stmt, err := sql.Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		_, err = BindWrite(stmt, false)
		detail, ok := errors.AsType[*Error](err)
		if !ok || detail.Code != code {
			t.Fatalf("%s: error = %v, want %s", input, err, code)
		}
	}
	stmt, _ := sql.Parse("DELETE FROM deployments WHERE TRUE")
	if _, err := BindWrite(stmt, true); err == nil {
		t.Fatal("all-namespaces write accepted")
	}
}

func TestPatchIsExactReplacementWithVersionTest(t *testing.T) {
	w := bindWrite(t, `UPDATE deployments SET replicas = 2, labels = '{"new":"only"}', annotations = '{}' WHERE name = 'web'`)
	object := writeObject("web")
	before := object.DeepCopy()
	patch, err := w.patch(object)
	if err != nil {
		t.Fatal(err)
	}
	var operations []patchOperation
	if err := json.Unmarshal(patch, &operations); err != nil {
		t.Fatal(err)
	}
	if len(operations) != 4 || operations[0].Op != "test" || operations[0].Path != "/metadata/resourceVersion" || operations[0].Value != "12" {
		t.Fatalf("patch = %s", patch)
	}
	if operations[2].Op != "replace" || !reflect.DeepEqual(operations[2].Value, map[string]any{"new": "only"}) || operations[3].Op != "add" {
		t.Fatalf("map must be replaced, not merged: %s", patch)
	}
	if !reflect.DeepEqual(object.Object, before.Object) {
		t.Fatal("patch generation mutated the input")
	}
}

func TestWritePartialFailureContinues(t *testing.T) {
	for _, input := range []string{"UPDATE deployments SET replicas = 2 WHERE TRUE", "DELETE FROM deployments WHERE TRUE"} {
		backend := &stubWriter{objects: []unstructured.Unstructured{writeObject("bad"), writeObject("good")}, fail: "bad"}
		result, err := bindWrite(t, input).Execute(context.Background(), backend, "test")
		if err != nil || result.AffectedRows != 1 || result.FailedRows != 1 || result.Errors[0].Name != "bad" || result.Errors[0].Namespace != "test" || result.Errors[0].Resource != "deployments" {
			t.Fatalf("result = %+v, error = %v", result, err)
		}
		if result.Errors[0].Reason != "Conflict" && result.Errors[0].Reason != "Forbidden" {
			t.Fatalf("reason = %s", result.Errors[0].Reason)
		}
		if len(backend.patches)+len(backend.deleted) != 2 || backend.namespace != "test" {
			t.Fatalf("did not process both objects in one namespace: %+v", backend)
		}
	}
}

func TestWhereErrorsAbortBeforeWrites(t *testing.T) {
	invalid := writeObject("invalid")
	invalid.Object["spec"] = map[string]any{"replicas": "wrong-type"}
	backend := &stubWriter{objects: []unstructured.Unstructured{writeObject("valid"), invalid}}
	w := bindWrite(t, "UPDATE deployments SET replicas = 2 WHERE replicas >= 0")
	if result, err := w.Execute(context.Background(), backend, "test"); err == nil || result != nil || len(backend.patches) != 0 {
		t.Fatalf("WHERE error wrote objects: result = %+v, error = %v", result, err)
	}
}

func TestZeroMatchesAndClusterScope(t *testing.T) {
	backend := &stubWriter{}
	result, err := bindWrite(t, "DELETE FROM namespaces WHERE name = 'absent'").Execute(context.Background(), backend, "unrelated")
	data, _ := json.Marshal(result)
	if err != nil || string(data) != `{"affected_rows":0}` || backend.namespace != "" {
		t.Fatalf("result = %s, scope = %q, error = %v", data, backend.namespace, err)
	}
}

func TestIngressMissingBackendFailsOnlyThatObject(t *testing.T) {
	w := bindWrite(t, "UPDATE ingresses SET default_backend_service = 'web-v2' WHERE TRUE")
	good := writeObject("good")
	good.Object["spec"] = map[string]any{"defaultBackend": map[string]any{"service": map[string]any{"name": "web", "port": map[string]any{"number": int64(80)}}}}
	backend := &stubWriter{objects: []unstructured.Unstructured{writeObject("missing"), good}}
	result, err := w.Execute(context.Background(), backend, "test")
	if err != nil || result.AffectedRows != 1 || result.FailedRows != 1 || result.Errors[0].Reason != "E_DEFAULT_BACKEND" || len(backend.patches) != 1 {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	var operations []patchOperation
	if err := json.Unmarshal(backend.patches["good"], &operations); err != nil || len(operations) != 2 || operations[1].Path != "/spec/defaultBackend/service/name" {
		t.Fatalf("Ingress patch touches more than its name: %s", backend.patches["good"])
	}
}
