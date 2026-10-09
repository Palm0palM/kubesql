package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/Palm0palM/kubesql/internal/resource"
	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func configMapDescriptor() resource.Descriptor {
	return resource.Descriptor{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, Verbs: []string{"list", "create", "patch", "delete"}}
}

func genericWrite(t *testing.T, input string) *Write {
	t.Helper()
	stmt, err := sql.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	w, err := BindWrite(stmt, false, configMapDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestPointerProjectionAndDynamicWhere(t *testing.T) {
	object := writeObject("settings")
	object.Object["data"] = map[string]any{"a/b": "slash", "a~b": "tilde", "big": int64(9007199254740993), "entries": []any{map[string]any{"value": "first"}}, "null": nil}
	stmt, _ := sql.Parse(`SELECT "/data/a~1b" AS slash, "/data/a~0b" AS tilde, "/data/entries/0/value" AS first, "/data/entries" AS entries, "/absent" AS absent FROM configmaps WHERE "/data/big" > 9007199254740992 AND "/data/null" IS NULL`)
	q, err := Bind(stmt, configMapDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := q.Execute(context.Background(), &stubLister{objects: []unstructured.Unstructured{object}}, "test", false)
	if err != nil || len(rows) != 1 || rows[0]["slash"] != "slash" || rows[0]["tilde"] != "tilde" || rows[0]["first"] != "first" || rows[0]["absent"] != nil || !reflect.DeepEqual(rows[0]["entries"], object.Object["data"].(map[string]any)["entries"]) {
		t.Fatalf("rows=%v, error=%v", rows, err)
	}
	for _, input := range []string{`SELECT name AS x, namespace AS x FROM configmaps`, `SELECT "/bad~2" FROM configmaps`, `SELECT "/bad~" FROM configmaps`} {
		stmt, _ := sql.Parse(input)
		if _, err := Bind(stmt, configMapDescriptor()); err == nil {
			t.Fatalf("invalid projection accepted: %s", input)
		}
	}
	stmt, _ = sql.Parse(`SELECT name FROM configmaps WHERE "/data/entries" = 3`)
	q, _ = Bind(stmt, configMapDescriptor())
	if _, err := q.Execute(context.Background(), &stubLister{objects: []unstructured.Unstructured{object}}, "test", false); err == nil {
		t.Fatal("object comparison accepted")
	}
	stmt, _ = sql.Parse(`SELECT name FROM configmaps WHERE "/data/entries" = NULL`)
	q, _ = Bind(stmt, configMapDescriptor())
	if _, err := q.Execute(context.Background(), &stubLister{objects: []unstructured.Unstructured{object}}, "test", false); err == nil {
		t.Fatal("object/NULL comparison accepted")
	}
}

func TestGenericPatchValuesAndExactPaths(t *testing.T) {
	object := writeObject("settings")
	object.Object["data"] = map[string]any{"old": "remove", "json": nil, "a/b": "before", "array": []any{"before"}}
	w := genericWrite(t, `UPDATE configmaps SET "/data/new" = 'plain', "/data/old" = NULL, "/data/json" = CAST('null' AS JSON), "/data/a~1b" = CAST('9007199254740993' AS JSON), "/data/array/0" = 'after' WHERE TRUE`)
	patch, err := w.patch(object)
	if err != nil {
		t.Fatal(err)
	}
	var ops []patchOperation
	if err := json.Unmarshal(patch, &ops); err != nil {
		t.Fatal(err)
	}
	if len(ops) != 6 || ops[1].Op != "add" || ops[2].Op != "remove" || ops[3].Op != "replace" || ops[3].Value != nil || ops[4].Path != "/data/a~1b" || ops[5].Op != "replace" {
		t.Fatalf("patch=%s", patch)
	}
	var raw []map[string]json.RawMessage
	_ = json.Unmarshal(patch, &raw)
	if string(raw[4]["value"]) != "9007199254740993" {
		t.Fatalf("integer rounded: %s", patch)
	}
	for _, value := range []string{`CAST('{"mode":"prod"}' AS JSON)`, `CAST('[1,true,null]' AS JSON)`, `CAST('true' AS JSON)`, `'{}'`} {
		w := genericWrite(t, `UPDATE configmaps SET "/data" = `+value+` WHERE TRUE`)
		if _, err := w.patch(object); err != nil {
			t.Fatal(err)
		}
	}
	missing := genericWrite(t, `UPDATE configmaps SET "/data/absent" = NULL WHERE TRUE`)
	patch, err = missing.patch(object)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(patch, &ops)
	if len(ops) != 1 {
		t.Fatalf("missing leaf removal should be no-op: %s", patch)
	}
	for _, path := range []string{`/missing/leaf`, `/data/array/1`, `/data/array/01`} {
		w := genericWrite(t, `UPDATE configmaps SET "`+path+`" = 'value' WHERE TRUE`)
		if _, err := w.patch(object); err == nil {
			t.Fatalf("unsafe parent/array write accepted: %s", path)
		}
	}
}

func TestProtectedAndOverlappingWrites(t *testing.T) {
	for _, path := range []string{"", "/apiVersion", "/kind", "/status", "/status/phase", "/metadata", "/metadata/name", "/metadata/namespace", "/metadata/uid", "/metadata/resourceVersion", "/metadata/managedFields/0", "/metadata/generation", "/metadata/deletionTimestamp"} {
		stmt, _ := sql.Parse(`UPDATE configmaps SET "` + path + `" = NULL WHERE TRUE`)
		_, err := BindWrite(stmt, false, configMapDescriptor())
		var detail *Error
		if !errors.As(err, &detail) || detail.Code != "E_WRITE_PROTECTED" {
			t.Fatalf("path=%s, error=%v", path, err)
		}
	}
	for _, input := range []string{
		`UPDATE configmaps SET "/data" = CAST('{}' AS JSON), "/data/mode" = 'prod' WHERE TRUE`,
		`UPDATE configmaps SET labels = '{}', "/metadata/labels/a" = 'x' WHERE TRUE`,
		`UPDATE configmaps SET "/data/mode" = 'a', "/data/mode" = 'b' WHERE TRUE`,
		`UPDATE configmaps SET "/data" = CAST('{bad-secret}' AS JSON) WHERE TRUE`,
		`UPDATE configmaps SET "/data" = 9223372036854775808 WHERE TRUE`,
	} {
		stmt, _ := sql.Parse(input)
		if _, err := BindWrite(stmt, false, configMapDescriptor()); err == nil {
			t.Fatalf("invalid assignment accepted: %s", input)
		}
	}
	// Decode once, then re-encode once. These keys are not protected ancestors.
	for _, path := range []string{"/metadata/labels/a~1b", "/metadata/annotations/a~0b", "/metadata~1uid", "/metadata/labels/name"} {
		if protectedPath(mustPointer(t, path)) {
			t.Fatalf("valid key incorrectly protected: %s", path)
		}
	}
}

func mustPointer(t *testing.T, path string) []string {
	t.Helper()
	parts, err := pointerPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return parts
}

func TestDiscoveryVerbBindingAndClusterNamespace(t *testing.T) {
	for _, input := range []string{`SELECT name FROM configmaps`, `UPDATE configmaps SET "/data/a" = 'x' WHERE TRUE`, `DELETE FROM configmaps WHERE TRUE`, `INSERT INTO configmaps (manifest) VALUES ('{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"web"}}')`} {
		stmt, _ := sql.Parse(input)
		d := configMapDescriptor()
		d.Verbs = []string{}
		var err error
		switch stmt.Type {
		case "select":
			_, err = Bind(stmt, d)
		case "insert":
			_, err = BindInsert(stmt, false, d)
		default:
			_, err = BindWrite(stmt, false, d)
		}
		var detail *Error
		if !errors.As(err, &detail) || detail.Code != "E_UNSUPPORTED_VERB" {
			t.Fatalf("input=%s, error=%v", input, err)
		}
	}
	d := resource.Descriptor{GVR: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, Kind: "Node", Verbs: []string{"list"}}
	stmt, _ := sql.Parse(`SELECT name, namespace FROM nodes`)
	q, err := Bind(stmt, d)
	if err != nil {
		t.Fatal(err)
	}
	backend := &stubLister{objects: []unstructured.Unstructured{writeObject("node")}}
	rows, err := q.Execute(context.Background(), backend, "unrelated", false)
	if err != nil || backend.namespace != "" || rows[0]["namespace"] != nil {
		t.Fatalf("rows=%v, scope=%s, error=%v", rows, backend.namespace, err)
	}
}

func TestGenericInsertAndJSONNumbers(t *testing.T) {
	stmt, _ := sql.Parse(`INSERT INTO "v1/configmaps" (manifest) VALUES ('{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings"},"data":{"mode":"new"}}')`)
	i, err := BindInsert(stmt, false, configMapDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	backend := &stubCreator{}
	result, err := i.Execute(context.Background(), backend, "selected")
	if err != nil || result.AffectedRows != 1 || backend.object.GetNamespace() != "selected" || backend.gvr.Resource != "configmaps" {
		t.Fatalf("result=%+v, error=%v", result, err)
	}
	for _, text := range []string{"9007199254740993", "9007199254740993.0", "9.007199254740993e15"} {
		value, err := parseJSON(text)
		if err != nil || value != int64(9007199254740993) {
			t.Fatalf("number=%s, value=%v, error=%v", text, value, err)
		}
	}
	for _, text := range []string{"9223372036854775808", "-9223372036854775809", "1e100", "{} null"} {
		if _, err := parseJSON(text); err == nil {
			t.Fatalf("out-of-range/trailing JSON accepted: %s", text)
		}
	}
}
