package engine

import (
	"context"
	"slices"

	"github.com/Palm0palM/kubesql/internal/resource"
	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Insert holds a locally checked manifest. Namespace resolution precedes Create.
type Insert struct {
	table  table
	object unstructured.Unstructured
}

type Creator interface {
	Create(context.Context, schema.GroupVersionResource, unstructured.Unstructured) error
}

func manifestError(message string) error { return &Error{Code: "E_MANIFEST", Message: message} }

func BindInsert(stmt *sql.Statement, allNamespaces bool, resolved ...resource.Descriptor) (*Insert, error) {
	if stmt.Type != "insert" {
		return nil, &Error{Code: "E_STATEMENT", Message: "expected INSERT"}
	}
	if allNamespaces {
		return nil, &Error{Code: "E_NAMESPACE", Message: "writes do not accept --all-namespaces"}
	}
	t, err := bindTable(stmt, resolved)
	if err != nil {
		return nil, err
	}
	if err := requireVerbs(t, "create"); err != nil {
		return nil, err
	}
	if len(stmt.Columns) != 1 || stmt.Columns[0].Name != "manifest" || len(stmt.Values) != 1 {
		return nil, &Error{Code: "E_INSERT_COLUMNS", Message: "INSERT requires only (manifest) and one value"}
	}
	literal, ok := stmt.Values[0].(*sql.Literal)
	if !ok {
		return nil, typeError("manifest requires a SQL string containing a JSON object")
	}
	text, ok := literal.Value.(string)
	if !ok {
		return nil, typeError("manifest requires a SQL string containing a JSON object")
	}
	var object unstructured.Unstructured
	raw, err := parseJSON(text)
	object.Object, ok = raw.(map[string]any)
	if err != nil || !ok {
		return nil, manifestError("manifest must be exactly one valid JSON object")
	}
	kinds := map[string]string{"namespaces": "Namespace", "deployments": "Deployment", "ingresses": "Ingress"}
	kind := t.kind
	if kind == "" {
		kind = kinds[stmt.Table]
	}
	if kind == "" && t.verbs != nil {
		return nil, &Error{Code: "E_DISCOVERY", Message: "discovery did not provide the resource kind"}
	}
	if kind != "" && (object.GetAPIVersion() != t.gvr.GroupVersion().String() || object.GetKind() != kind) {
		return nil, manifestError("manifest apiVersion/kind must match the target table")
	}
	metadata, ok := object.Object["metadata"].(map[string]any)
	// Unknown resources defer the persistent/request distinction until Discovery.
	// A create-only API (no get/list) submits a request, not a named stored object.
	deferred := kind == "" && t.verbs == nil
	requestOnly := t.verbs != nil && !slices.Contains(t.verbs, "get") && !slices.Contains(t.verbs, "list")
	if _, exists := object.Object["metadata"]; exists && !ok {
		return nil, manifestError("manifest requires object metadata")
	}
	name, ok := metadata["name"].(string)
	if !deferred && !requestOnly && (!ok || name == "") {
		return nil, manifestError("manifest requires nonempty metadata.name; generateName is unsupported")
	}
	if _, exists := metadata["generateName"]; exists {
		return nil, manifestError("generateName is unsupported")
	}
	if _, exists := object.Object["status"]; exists {
		return nil, manifestError("status is server-managed")
	}
	for _, field := range []string{"uid", "resourceVersion", "managedFields", "generation", "creationTimestamp", "deletionTimestamp", "deletionGracePeriodSeconds", "selfLink"} {
		if _, exists := metadata[field]; exists {
			return nil, manifestError("server-managed metadata field is forbidden: " + field)
		}
	}
	if raw, exists := metadata["namespace"]; exists {
		ns, ok := raw.(string)
		if !ok {
			return nil, manifestError("metadata.namespace must be a string")
		}
		if !t.namespaced && ns != "" {
			return nil, &Error{Code: "E_NAMESPACE", Message: "cluster-scoped manifest must not specify a nonempty namespace"}
		}
	}
	return &Insert{table: t, object: object}, nil
}

func (i *Insert) Execute(ctx context.Context, client Creator, namespace string) (*WriteResult, error) {
	if err := i.checkNamespace(namespace); err != nil {
		return nil, err
	}
	object := *i.object.DeepCopy()
	if i.table.namespaced {
		object.SetNamespace(namespace)
	}
	result := &WriteResult{}
	if err := client.Create(ctx, i.table.gvr, object); err != nil {
		result.addFailure(i.table.gvr, object, err)
	} else {
		result.AffectedRows = 1
	}
	return result, nil
}

func (i *Insert) checkNamespace(namespace string) error {
	if i.table.namespaced && (namespace == "" || (i.object.GetNamespace() != "" && i.object.GetNamespace() != namespace)) {
		return &Error{Code: "E_NAMESPACE", Message: "manifest namespace must match the resolved CLI namespace"}
	}
	return nil
}
