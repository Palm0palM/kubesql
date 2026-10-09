package engine

import (
	"encoding/json"
	"io"
	"math/big"
	"strconv"
	"strings"

	"github.com/Palm0palM/kubesql/internal/sql"
)

func parseJSON(text string) (any, error) {
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	var raw any
	if err := d.Decode(&raw); err != nil {
		return nil, typeError("invalid JSON value")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, typeError("expected exactly one JSON value")
	}
	return jsonNumbers(raw)
}

func jsonNumbers(raw any) (any, error) {
	switch v := raw.(type) {
	case json.Number:
		n, ok := new(big.Rat).SetString(string(v))
		if !ok {
			return nil, typeError("invalid JSON number")
		}
		if n.IsInt() {
			if !n.Num().IsInt64() {
				return nil, typeError("JSON integer exceeds int64 range")
			}
			return n.Num().Int64(), nil
		}
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return nil, typeError("JSON number exceeds supported range")
		}
		return f, nil
	case map[string]any:
		for key, entry := range v {
			converted, err := jsonNumbers(entry)
			if err != nil {
				return nil, err
			}
			v[key] = converted
		}
	case []any:
		for index, entry := range v {
			converted, err := jsonNumbers(entry)
			if err != nil {
				return nil, err
			}
			v[index] = converted
		}
	}
	return raw, nil
}

func assignmentValue(expr sql.Expression) (any, bool, error) {
	switch e := expr.(type) {
	case *sql.JSONCast:
		value, err := parseJSON(e.Text)
		return value, false, err
	case *sql.Literal:
		value, err := jsonNumbers(e.Value)
		return value, e.Value == nil, err
	default:
		return nil, false, typeError("SET accepts literal values or CAST(string AS JSON) only")
	}
}
