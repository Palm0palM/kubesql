package engine

import (
	"encoding/json"
	"math/big"
	"strconv"
)

type valueKind uint8

const (
	nullKind valueKind = iota
	stringKind
	numberKind
	boolKind
	objectKind
)

type truth uint8

const (
	unknown truth = iota
	falseTruth
	trueTruth
)

type value struct {
	kind   valueKind
	scalar any // string, *big.Rat, or truth; nil for SQL NULL.
}

func typeError(message string) error { return &Error{Code: "E_TYPE", Message: message} }

func scalarValue(raw any) (value, error) {
	var number string
	switch v := raw.(type) {
	case nil:
		return value{kind: nullKind}, nil
	case string:
		return value{kind: stringKind, scalar: v}, nil
	case bool:
		if v {
			return boolean(trueTruth), nil
		}
		return boolean(falseTruth), nil
	case int64:
		number = strconv.FormatInt(v, 10)
	case int:
		number = strconv.Itoa(v)
	case float64:
		number = strconv.FormatFloat(v, 'g', -1, 64)
	case json.Number:
		number = string(v)
	default:
		return value{}, typeError("WHERE comparisons require scalar values")
	}
	rat, ok := new(big.Rat).SetString(number)
	if !ok {
		return value{}, typeError("invalid numeric value")
	}
	return value{kind: numberKind, scalar: rat}, nil
}

func boolean(t truth) value { return value{kind: boolKind, scalar: t} }

func (v value) isNull() bool {
	return v.kind == nullKind || (v.kind == boolKind && v.scalar == unknown)
}

func (v value) asTruth() (truth, error) {
	if v.isNull() {
		return unknown, nil
	}
	if v.kind != boolKind {
		return unknown, typeError("logical operators and WHERE require boolean values")
	}
	return v.scalar.(truth), nil
}

func not(t truth) truth {
	switch t {
	case trueTruth:
		return falseTruth
	case falseTruth:
		return trueTruth
	default:
		return unknown
	}
}

func and(left, right truth) truth {
	if left == falseTruth || right == falseTruth {
		return falseTruth
	}
	if left == unknown || right == unknown {
		return unknown
	}
	return trueTruth
}

func or(left, right truth) truth {
	if left == trueTruth || right == trueTruth {
		return trueTruth
	}
	if left == unknown || right == unknown {
		return unknown
	}
	return falseTruth
}
