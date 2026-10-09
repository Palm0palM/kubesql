package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/Palm0palM/kubesql/internal/resource"
	"github.com/Palm0palM/kubesql/internal/sql"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

type assignment struct {
	column string
	path   []string
	value  any
	remove bool
}

type Write struct {
	query       *Query
	operation   string
	assignments []assignment
}

type RowError struct {
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Reason    string `json:"reason"`
}

type WriteResult struct {
	AffectedRows int        `json:"affected_rows"`
	FailedRows   int        `json:"failed_rows,omitempty"`
	Errors       []RowError `json:"errors,omitempty"`
}

// Writer is the minimal backend needed by conditional UPDATE/DELETE.
type Writer interface {
	Lister
	Patch(context.Context, schema.GroupVersionResource, unstructured.Unstructured, []byte) error
	Delete(context.Context, schema.GroupVersionResource, unstructured.Unstructured) error
}

func BindWrite(stmt *sql.Statement, allNamespaces bool, resolved ...resource.Descriptor) (*Write, error) {
	if stmt.Type != "update" && stmt.Type != "delete" {
		return nil, &Error{Code: "E_STATEMENT", Message: "expected UPDATE or DELETE"}
	}
	if stmt.Where == nil {
		return nil, &Error{Code: "E_WHERE_REQUIRED", Message: "UPDATE/DELETE require WHERE"}
	}
	if allNamespaces {
		return nil, &Error{Code: "E_NAMESPACE", Message: "writes do not accept --all-namespaces"}
	}
	query, err := Bind(stmt, resolved...)
	if err != nil {
		return nil, err
	}
	write := &Write{query: query, operation: stmt.Type}
	verb := "patch"
	if stmt.Type == "delete" {
		verb = "delete"
	}
	if err := requireVerbs(query.table, "list", verb); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, set := range stmt.Assignments {
		if seen[set.Column] {
			return nil, &Error{Code: "E_DUPLICATE_COLUMN", Message: "duplicate assignment column " + set.Column}
		}
		seen[set.Column] = true
		var a assignment
		if set.Quoted && (set.Column == "" || strings.HasPrefix(set.Column, "/")) {
			path, pathErr := pointerPath(set.Column)
			if pathErr != nil {
				return nil, pathErr
			}
			if protectedPath(path) {
				return nil, &Error{Code: "E_WRITE_PROTECTED", Message: "cannot modify protected resource fields or their ancestors"}
			}
			value, remove, valueErr := assignmentValue(set.Value)
			if valueErr != nil {
				return nil, valueErr
			}
			a = assignment{column: set.Column, path: path, value: value, remove: remove}
		} else {
			known := ""
			for name, t := range tables {
				if t.gvr == query.table.gvr {
					known = name
				}
			}
			a, err = bindAssignment(known, set)
		}
		if err != nil {
			return nil, err
		}
		for _, earlier := range write.assignments {
			if pathsOverlap(earlier.path, a.path) {
				return nil, &Error{Code: "E_OVERLAPPING_ASSIGNMENTS", Message: "assignment paths overlap"}
			}
		}
		write.assignments = append(write.assignments, a)
	}
	return write, nil
}

func bindAssignment(table string, set sql.Assignment) (assignment, error) {
	allowed := set.Column == "labels" || set.Column == "annotations" ||
		(table == "deployments" && set.Column == "replicas") ||
		(table == "ingresses" && set.Column == "default_backend_service")
	if !allowed {
		return assignment{}, &Error{Code: "E_WRITE_COLUMN", Message: "column is not writable: " + set.Column}
	}
	literal, ok := set.Value.(*sql.Literal)
	if !ok {
		return assignment{}, typeError("SET accepts literal values only")
	}
	a := assignment{column: set.Column, value: literal.Value}
	switch set.Column {
	case "replicas":
		v, err := scalarValue(literal.Value)
		if err != nil || v.kind != numberKind {
			return assignment{}, typeError("replicas must be a nonnegative integer")
		}
		number := v.scalar.(*big.Rat)
		if !number.IsInt() || number.Sign() < 0 || number.Cmp(new(big.Rat).SetInt64(2147483647)) > 0 {
			return assignment{}, typeError("replicas must be an integer between 0 and 2147483647")
		}
		a.path, a.value = []string{"spec", "replicas"}, number.Num().Int64()
	case "default_backend_service":
		name, ok := literal.Value.(string)
		if !ok || len(validation.IsDNS1035Label(name)) != 0 {
			return assignment{}, typeError("default_backend_service must be a valid nonempty Service name")
		}
		a.path = []string{"spec", "defaultBackend", "service", "name"}
	case "labels", "annotations":
		if literal.Value == nil {
			return assignment{column: set.Column, path: []string{"metadata", set.Column}, remove: true}, nil
		}
		text, ok := literal.Value.(string)
		if !ok {
			return assignment{}, typeError("labels/annotations require a SQL string containing a JSON object of strings")
		}
		mapping, err := stringMap(text)
		if err != nil {
			return assignment{}, err
		}
		a.path, a.value = []string{"metadata", set.Column}, mapping
	}
	return a, nil
}

func stringMap(text string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, typeError("invalid labels/annotations JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, typeError("labels/annotations must contain exactly one JSON object")
	}
	mapping, ok := raw.(map[string]any)
	if !ok {
		return nil, typeError("labels/annotations must be a JSON object of strings")
	}
	for _, v := range mapping {
		if _, ok := v.(string); !ok {
			return nil, typeError("labels/annotations values must be strings")
		}
	}
	return mapping, nil
}

func (w *Write) Execute(ctx context.Context, client Writer, namespace string) (*WriteResult, error) {
	if w.query.table.namespaced && namespace == "" {
		return nil, &Error{Code: "E_NAMESPACE", Message: "writes require one resolved namespace"}
	}
	objects, err := w.query.candidates(ctx, client, namespace, false)
	if err != nil {
		return nil, err
	}
	result := &WriteResult{}
	for _, object := range objects {
		if w.operation == "delete" {
			err = client.Delete(ctx, w.query.table.gvr, object)
		} else {
			var patch []byte
			patch, err = w.patch(object)
			if err == nil {
				err = client.Patch(ctx, w.query.table.gvr, object, patch)
			}
		}
		if err == nil {
			result.AffectedRows++
			continue
		}
		result.addFailure(w.query.table.gvr, object, err)
	}
	return result, nil
}

// addFailure exposes safe reasons, not API messages that may contain field values.
func (r *WriteResult) addFailure(gvr schema.GroupVersionResource, object unstructured.Unstructured, err error) {
	reason := string(apierrors.ReasonForError(err))
	if detail, ok := err.(*Error); ok {
		reason = detail.Code
	} else if errors.Is(err, context.DeadlineExceeded) {
		reason = "Timeout"
	} else if errors.Is(err, context.Canceled) {
		reason = "Cancelled"
	} else if reason == "Unknown" {
		reason = "APIRequestFailed"
	}
	r.Errors = append(r.Errors, RowError{
		Resource: gvr.Resource, Namespace: object.GetNamespace(),
		Name: object.GetName(), Reason: reason,
	})
	r.FailedRows++
}

type patchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func (w *Write) patch(object unstructured.Unstructured) ([]byte, error) {
	if object.GetResourceVersion() == "" {
		return nil, &Error{Code: "E_RESOURCE_VERSION", Message: "missing resourceVersion"}
	}
	operations := []patchOperation{{"test", "/metadata/resourceVersion", object.GetResourceVersion()}}
	for _, a := range w.assignments {
		if a.column == "default_backend_service" {
			name, found, err := unstructured.NestedString(object.Object, a.path...)
			if err != nil || !found || name == "" {
				return nil, &Error{Code: "E_DEFAULT_BACKEND", Message: "Ingress has no existing Service default backend"}
			}
		}
		parent, found, err := pointerRead(object.Object, a.path[:len(a.path)-1])
		if err != nil || !found || parent == nil {
			return nil, &Error{Code: "E_FIELD_PARENT", Message: "assignment parent is not an existing object"}
		}
		exists := false
		switch p := parent.(type) {
		case map[string]any:
			_, exists = p[a.path[len(a.path)-1]]
		case []any:
			index, err := arrayIndex(a.path[len(a.path)-1])
			if err != nil {
				return nil, err
			}
			if index >= len(p) || a.remove {
				return nil, &Error{Code: "E_ARRAY_WRITE", Message: "array writes only replace existing indices; insertion/removal is unsupported"}
			}
			exists = true
		default:
			return nil, &Error{Code: "E_FIELD_PARENT", Message: "assignment parent is not an existing object or array"}
		}
		if a.remove {
			if exists {
				operations = append(operations, patchOperation{Op: "remove", Path: encodePointer(a.path)})
			}
			continue
		}
		op := "add"
		if exists {
			op = "replace"
		}
		operations = append(operations, patchOperation{Op: op, Path: encodePointer(a.path), Value: a.value})
	}
	patch, err := json.Marshal(operations)
	if err != nil {
		return nil, fmt.Errorf("cannot encode JSON patch")
	}
	return patch, nil
}
