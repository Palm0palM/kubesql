package engine

import (
	"context"
	"testing"

	"github.com/Palm0palM/kubesql/internal/resource"
	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestCreateOnlyRequestWithoutName(t *testing.T) {
	d := resource.Descriptor{GVR: schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectaccessreviews"}, Kind: "SelfSubjectAccessReview", Verbs: []string{"create"}}
	stmt, err := sql.Parse(`INSERT INTO "authorization.k8s.io/v1/selfsubjectaccessreviews" (manifest) VALUES ('{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","spec":{"resourceAttributes":{"verb":"list","resource":"pods"}}}')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := Preflight(stmt, false, "unrelated"); err != nil {
		t.Fatal(err)
	}
	i, err := BindInsert(stmt, false, d)
	if err != nil {
		t.Fatal(err)
	}
	backend := &stubCreator{}
	result, err := i.Execute(context.Background(), backend, "unrelated")
	if err != nil || result.AffectedRows != 1 || backend.calls != 1 || backend.object.GetName() != "" || backend.object.GetNamespace() != "" {
		t.Fatalf("result=%+v,error=%v", result, err)
	}
	if _, exists := backend.object.Object["metadata"]; exists {
		t.Fatal("invented metadata for cluster-scoped request")
	}
	// The same object is still invalid when Discovery identifies a stored resource.
	d.Verbs = []string{"create", "get"}
	if _, err := BindInsert(stmt, false, d); err == nil {
		t.Fatal("get-capable stored resource accepted missing name")
	}
	d.Verbs = []string{"create", "list"}
	if _, err := BindInsert(stmt, false, d); err == nil {
		t.Fatal("list-capable stored resource accepted missing name")
	}
}
