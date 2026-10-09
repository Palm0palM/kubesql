package engine

import (
	"math/big"
	"strings"

	"github.com/Palm0palM/kubesql/internal/sql"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// boundExpression resolves columns/types once, without accessing the API.
type boundExpression struct {
	kind        valueKind
	literal     *value
	field       *column
	operator    string
	left, right *boundExpression
}

func bindExpression(expr sql.Expression, t table) (*boundExpression, error) {
	switch e := expr.(type) {
	case *sql.Literal:
		v, err := scalarValue(e.Value)
		if err != nil {
			return nil, err
		}
		return &boundExpression{kind: v.kind, literal: &v}, nil
	case *sql.ColumnReference:
		for _, c := range t.columns {
			if c.name == e.Name {
				return &boundExpression{kind: c.kind, field: &c}, nil
			}
		}
		return nil, &Error{Code: "E_UNKNOWN_COLUMN", Message: "unknown WHERE column " + e.Name}
	case *sql.UnaryExpression:
		operand, err := bindExpression(e.Operand, t)
		if err != nil {
			return nil, err
		}
		if e.Operator == "NOT" && !booleanKind(operand.kind) {
			return nil, typeError("NOT requires a boolean value")
		}
		return &boundExpression{kind: boolKind, operator: e.Operator, left: operand}, nil
	case *sql.BinaryExpression:
		left, err := bindExpression(e.Left, t)
		if err != nil {
			return nil, err
		}
		right, err := bindExpression(e.Right, t)
		if err != nil {
			return nil, err
		}
		if e.Operator == "AND" || e.Operator == "OR" {
			if !booleanKind(left.kind) || !booleanKind(right.kind) {
				return nil, typeError("AND/OR require boolean values")
			}
		} else if left.kind != nullKind && right.kind != nullKind && left.kind != right.kind {
			return nil, typeError("comparison operands have incompatible types")
		}
		return &boundExpression{kind: boolKind, operator: e.Operator, left: left, right: right}, nil
	default:
		return nil, typeError("unsupported WHERE expression")
	}
}

func booleanKind(kind valueKind) bool { return kind == boolKind || kind == nullKind }

func readColumn(object unstructured.Unstructured, c column) (any, error) {
	value, _, err := unstructured.NestedFieldNoCopy(object.Object, c.path...)
	if err != nil {
		return nil, typeError("invalid structure for column " + c.name)
	}
	return value, nil
}

func (e *boundExpression) evaluate(object unstructured.Unstructured) (value, error) {
	if e.literal != nil {
		return *e.literal, nil
	}
	if e.field != nil {
		raw, err := readColumn(object, *e.field)
		if err != nil {
			return value{}, err
		}
		v, err := scalarValue(raw)
		if err == nil && v.kind != nullKind && v.kind != e.kind {
			err = typeError("unexpected value type for column " + e.field.name)
		}
		return v, err
	}
	left, err := e.left.evaluate(object)
	if err != nil {
		return value{}, err
	}
	switch e.operator {
	case "IS NULL", "IS NOT NULL":
		result := falseTruth
		if left.isNull() {
			result = trueTruth
		}
		if e.operator == "IS NOT NULL" {
			result = not(result)
		}
		return boolean(result), nil
	case "NOT":
		t, err := left.asTruth()
		return boolean(not(t)), err
	}
	// Evaluate both operands: do not hide malformed resource types via short-circuiting.
	right, err := e.right.evaluate(object)
	if err != nil {
		return value{}, err
	}
	if e.operator == "AND" || e.operator == "OR" {
		l, err := left.asTruth()
		if err != nil {
			return value{}, err
		}
		r, err := right.asTruth()
		if err != nil {
			return value{}, err
		}
		if e.operator == "AND" {
			return boolean(and(l, r)), nil
		}
		return boolean(or(l, r)), nil
	}
	return compare(e.operator, left, right)
}

func compare(operator string, left, right value) (value, error) {
	if left.isNull() || right.isNull() {
		return boolean(unknown), nil
	}
	if left.kind != right.kind {
		return value{}, typeError("comparison operands have incompatible types")
	}
	var order int
	switch left.kind {
	case numberKind:
		order = left.scalar.(*big.Rat).Cmp(right.scalar.(*big.Rat))
	case stringKind:
		order = strings.Compare(left.scalar.(string), right.scalar.(string))
	case boolKind:
		order = int(left.scalar.(truth)) - int(right.scalar.(truth))
	default:
		return value{}, typeError("comparison requires scalar values")
	}
	matched := false
	switch operator {
	case "=":
		matched = order == 0
	case "<>":
		matched = order != 0
	case ">":
		matched = order > 0
	case ">=":
		matched = order >= 0
	case "<":
		matched = order < 0
	case "<=":
		matched = order <= 0
	default:
		return value{}, typeError("unsupported comparison operator")
	}
	if matched {
		return boolean(trueTruth), nil
	}
	return boolean(falseTruth), nil
}
