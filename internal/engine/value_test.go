package engine

import (
	"encoding/json"
	"math"
	"testing"
)

func TestScalarNumberConversions(t *testing.T) {
	for _, raw := range []any{int64(2), int(2), float64(2), json.Number("2.0")} {
		v, err := scalarValue(raw)
		if err != nil {
			t.Fatal(err)
		}
		want, err := scalarValue(json.Number("2"))
		if err != nil {
			t.Fatal(err)
		}
		result, err := compare("=", v, want)
		if err != nil || result.scalar != trueTruth {
			t.Fatalf("%T(%v): result = %v, error = %v", raw, raw, result, err)
		}
	}
	for _, raw := range []any{math.Inf(1), math.NaN(), json.Number("not-a-number"), []any{}, map[string]any{}} {
		if _, err := scalarValue(raw); err == nil {
			t.Fatalf("%T must not be a valid scalar number", raw)
		}
	}
}
