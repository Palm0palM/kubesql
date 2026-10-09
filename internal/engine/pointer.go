package engine

import (
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func pointerPath(text string) ([]string, error) {
	if text == "" {
		return nil, nil
	}
	if !strings.HasPrefix(text, "/") {
		return nil, &Error{Code: "E_POINTER", Message: "JSON Pointer must start with /"}
	}
	parts := strings.Split(text[1:], "/")
	for i, part := range parts {
		var decoded strings.Builder
		for j := 0; j < len(part); j++ {
			if part[j] != '~' {
				decoded.WriteByte(part[j])
				continue
			}
			j++
			if j >= len(part) || (part[j] != '0' && part[j] != '1') {
				return nil, &Error{Code: "E_POINTER", Message: "invalid JSON Pointer escape"}
			}
			if part[j] == '0' {
				decoded.WriteByte('~')
			} else {
				decoded.WriteByte('/')
			}
		}
		parts[i] = decoded.String()
	}
	return parts, nil
}

func encodePointer(path []string) string {
	parts := make([]string, len(path))
	for i, part := range path {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
	}
	if len(parts) == 0 {
		return ""
	}
	return "/" + strings.Join(parts, "/")
}

func arrayIndex(text string) (int, error) {
	if text == "" || (len(text) > 1 && text[0] == '0') {
		return 0, &Error{Code: "E_POINTER", Message: "invalid array index"}
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, &Error{Code: "E_POINTER", Message: "invalid array index"}
		}
	}
	i, err := strconv.Atoi(text)
	if err != nil {
		return 0, &Error{Code: "E_POINTER", Message: "array index is out of range"}
	}
	return i, nil
}

func pointerRead(root any, path []string) (any, bool, error) {
	current := root
	for _, part := range path {
		switch v := current.(type) {
		case map[string]any:
			var found bool
			current, found = v[part]
			if !found {
				return nil, false, nil
			}
		case []any:
			i, err := arrayIndex(part)
			if err != nil {
				return nil, false, err
			}
			if i >= len(v) {
				return nil, false, nil
			}
			current = v[i]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func resolveColumn(name string, quoted bool, t table) (column, error) {
	if quoted && (name == "" || strings.HasPrefix(name, "/")) {
		path, err := pointerPath(name)
		if err != nil {
			return column{}, err
		}
		return column{name: name, path: path, kind: dynamicKind, pointer: true}, nil
	}
	for _, c := range t.columns {
		if c.name == name {
			return c, nil
		}
	}
	return column{}, &Error{Code: "E_UNKNOWN_COLUMN", Message: "unknown column " + name}
}

func readPointer(object unstructured.Unstructured, path []string) (any, error) {
	value, _, err := pointerRead(object.Object, path)
	return value, err
}

func protectedPath(path []string) bool {
	if len(path) == 0 {
		return true
	}
	if path[0] == "apiVersion" || path[0] == "kind" || path[0] == "status" {
		return true
	}
	if path[0] != "metadata" {
		return false
	}
	if len(path) == 1 {
		return true
	}
	return slices.Contains([]string{"name", "namespace", "uid", "resourceVersion", "managedFields", "generation", "creationTimestamp", "deletionTimestamp", "deletionGracePeriodSeconds", "selfLink", "generateName"}, path[1])
}

func pathsOverlap(a, b []string) bool {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
