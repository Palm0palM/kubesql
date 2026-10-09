package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Palm0palM/kubesql/internal/sql"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func insertSQL(table, text string) string {
	return "INSERT INTO " + table + " (manifest) VALUES ('" + strings.ReplaceAll(text, "'", "''") + "')"
}

func bindInsert(t *testing.T, table, text string) *Insert {
	t.Helper()
	stmt, err := sql.Parse(insertSQL(table, text))
	if err != nil {
		t.Fatal(err)
	}
	i, err := BindInsert(stmt, false)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

type stubCreator struct {
	calls  int
	gvr    schema.GroupVersionResource
	object unstructured.Unstructured
	err    error
}

func (s *stubCreator) Create(_ context.Context, gvr schema.GroupVersionResource, object unstructured.Unstructured) error {
	s.calls++
	s.gvr, s.object = gvr, object
	return s.err
}

func TestInsertManifestValidation(t *testing.T) {
	for _, text := range []string{
		`not json`, `[]`, `null`, `42`, `"object"`, `{} {}`, `{"apiVersion":"v1",}`,
		`{"apiVersion":"apps/v1","kind":"Namespace","metadata":{"name":"web"}}`,
		`{"apiVersion":"v1","kind":"Deployment","metadata":{"name":"web"}}`,
		`{"apiVersion":"v1","kind":"Namespace","metadata":[]}`,
		`{"apiVersion":"v1","kind":"Namespace","metadata":{}}`,
		`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":3}}`,
		`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":""}}`,
		`{"apiVersion":"v1","kind":"Namespace","metadata":{"generateName":"web-"}}`,
		`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"web","generateName":"web-"}}`,
		`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"web","namespace":"other"}}`,
		`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"web","namespace":null}}`,
		`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"web"},"status":null}`,
	} {
		stmt, err := sql.Parse(insertSQL("namespaces", text))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BindInsert(stmt, false); err == nil {
			t.Fatalf("invalid manifest accepted: %s", text)
		}
	}
	for _, field := range []string{"uid", "resourceVersion", "managedFields", "generation", "creationTimestamp", "deletionTimestamp", "deletionGracePeriodSeconds", "selfLink"} {
		stmt, _ := sql.Parse(insertSQL("namespaces", `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"web","`+field+`":null}}`))
		if _, err := BindInsert(stmt, false); err == nil {
			t.Fatalf("server field accepted: %s", field)
		}
	}
	for _, input := range []string{
		`INSERT INTO namespaces (name) VALUES ('web')`, `INSERT INTO namespaces (manifest, name) VALUES ('{}', 'web')`,
		`INSERT INTO namespaces (manifest) VALUES ('{}', '{}')`, `INSERT INTO namespaces (manifest) VALUES (NULL)`,
		`INSERT INTO namespaces (manifest) VALUES (manifest)`, `INSERT INTO missing (manifest) VALUES ('{}')`,
	} {
		stmt, err := sql.Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BindInsert(stmt, false); err == nil {
			t.Fatalf("invalid insert accepted: %s", input)
		}
	}
	stmt, _ := sql.Parse(insertSQL("namespaces", `{}`))
	if _, err := BindInsert(stmt, true); err == nil {
		t.Fatal("all-namespaces accepted")
	}
}

func TestInsertScopeAndPreservation(t *testing.T) {
	for _, tt := range []struct {
		table, version, kind string
		namespaced           bool
	}{
		{"namespaces", "v1", "Namespace", false}, {"deployments", "apps/v1", "Deployment", true}, {"ingresses", "networking.k8s.io/v1", "Ingress", true},
	} {
		text := `{"apiVersion":"` + tt.version + `","kind":"` + tt.kind + `","metadata":{"name":"web","annotations":{"owner":"alice's"}},"spec":{"replicas":2}}`
		i := bindInsert(t, tt.table, text)
		before := i.object.DeepCopy()
		backend := &stubCreator{}
		result, err := i.Execute(context.Background(), backend, "chosen")
		if err != nil || result.AffectedRows != 1 || backend.calls != 1 || backend.gvr != i.table.gvr {
			t.Fatalf("result = %+v, error = %v", result, err)
		}
		wantScope := ""
		if tt.namespaced {
			wantScope = "chosen"
		}
		if backend.object.GetNamespace() != wantScope || !reflect.DeepEqual(i.object.Object, before.Object) {
			t.Fatal("scope incorrect or input mutated")
		}
		if backend.object.Object["spec"].(map[string]any)["replicas"] != int64(2) {
			t.Fatal("integer became float64")
		}
		if backend.object.GetAnnotations()["owner"] != "alice's" {
			t.Fatal("SQL/JSON escaping changed value")
		}
	}
	for _, ns := range []string{"other", "chosen"} {
		i := bindInsert(t, "deployments", `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"chosen"}}`)
		backend := &stubCreator{}
		_, err := i.Execute(context.Background(), backend, ns)
		if (ns == "other" && (err == nil || backend.calls != 0)) || (ns == "chosen" && (err != nil || backend.calls != 1)) {
			t.Fatalf("namespace %s: calls=%d, error=%v", ns, backend.calls, err)
		}
	}
}

func TestInsertAPIFailureSummary(t *testing.T) {
	i := bindInsert(t, "ingresses", `{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"web"}}`)
	for _, apiErr := range []error{
		apierrors.NewAlreadyExists(i.table.gvr.GroupResource(), "web"),
		apierrors.NewForbidden(i.table.gvr.GroupResource(), "web", errors.New("sensitive-payload")),
	} {
		backend := &stubCreator{err: apiErr}
		result, err := i.Execute(context.Background(), backend, "chosen")
		if err != nil || backend.calls != 1 || result.AffectedRows != 0 || result.FailedRows != 1 || result.Errors[0].Resource != "ingresses" || result.Errors[0].Namespace != "chosen" || result.Errors[0].Name != "web" || result.Errors[0].Reason != string(apierrors.ReasonForError(apiErr)) {
			t.Fatalf("result = %+v, error = %v", result, err)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "sensitive-payload") {
			t.Fatal("API message leaked")
		}
	}
}

func TestManifestProjectionAndWriteProtection(t *testing.T) {
	object := writeObject("web")
	rows, err := bindQuery(t, "SELECT manifest FROM deployments WHERE manifest IS NOT NULL").Execute(context.Background(), &stubLister{objects: []unstructured.Unstructured{object}}, "test", false)
	if err != nil || !reflect.DeepEqual(rows[0]["manifest"], object.Object) {
		t.Fatalf("rows=%v, error=%v", rows, err)
	}
	stmt, _ := sql.Parse(`UPDATE deployments SET manifest = '{}' WHERE TRUE`)
	if _, err := BindWrite(stmt, false); err == nil {
		t.Fatal("whole manifest update accepted")
	}
	stmt, _ = sql.Parse(`SELECT manifest FROM deployments WHERE manifest = '{}'`)
	if _, err := Bind(stmt); err == nil {
		t.Fatal("object/scalar comparison accepted")
	}
}
