package engine

import (
	"slices"
	"strings"

	"github.com/Palm0palM/kubesql/internal/resource"
	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Preflight rejects syntax-independent column/value/safety errors before even
// Discovery. Unknown resources need Discovery only to bind kind/scope/verbs.
func Preflight(stmt *sql.Statement, all bool, namespace ...string) error {
	d := resource.Descriptor{GVR: schema.GroupVersionResource{Resource: stmt.Table}, Namespaced: true}
	if stmt.TableQuoted {
		parts := strings.Split(stmt.Table, "/")
		switch len(parts) {
		case 2:
			d.GVR = schema.GroupVersionResource{Version: parts[0], Resource: parts[1]}
		case 3:
			d.GVR = schema.GroupVersionResource{Group: parts[0], Version: parts[1], Resource: parts[2]}
		default:
			return &Error{Code: "E_TABLE", Message: "invalid exact table name or unsupported subresource"}
		}
		if slices.Contains(parts, "") {
			return &Error{Code: "E_TABLE", Message: "empty exact table segment"}
		}
	} else if known, ok := tables[stmt.Table]; ok {
		d.GVR = known.gvr
	}
	for name, known := range tables {
		if d.GVR == known.gvr {
			d.Namespaced = known.namespaced
			d.Kind = map[string]string{"namespaces": "Namespace", "deployments": "Deployment", "ingresses": "Ingress"}[name]
		}
	}
	var err error
	switch stmt.Type {
	case "select":
		_, err = Bind(stmt, d)
	case "insert":
		var insert *Insert
		insert, err = BindInsert(stmt, all, d)
		if err == nil && len(namespace) != 0 {
			err = insert.checkNamespace(namespace[0])
		}
	default:
		_, err = BindWrite(stmt, all, d)
	}
	return err
}
