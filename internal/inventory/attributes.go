package inventory

import (
	"encoding/json"
	"math"
	"strconv"
)

type AttributeFilter struct {
	Key   string `json:"key"`
	Op    string `json:"op" jsonschema:"eq, contains (string only), gt, gte, lt, lte (number only)"`
	Value any    `json:"value" jsonschema:"String for exact textual comparison; number for strict numeric-string comparison. Unit-bearing strings are never numeric."`
}

func attributeSQL(filters []AttributeFilter) (string, []any, error) {
	if len(filters) > 20 {
		return "", nil, invalid("At most 20 attribute filters are allowed")
	}
	query := ""
	args := []any{}
	for _, f := range filters {
		if f.Key == "" || len(f.Key) > 100 {
			return "", nil, invalid("Attribute key must be 1–100 characters")
		}
		clause := ` AND EXISTS(SELECT 1 FROM json_each(i.attributes) af WHERE af.key=? AND `
		args = append(args, f.Key)
		if text, ok := f.Value.(string); ok {
			if len(text) > 2000 {
				return "", nil, invalid("Attribute string value is too long")
			}
			switch f.Op {
			case "eq":
				clause += `af.value=? COLLATE BINARY`
			case "contains":
				clause += `instr(af.value,?)>0`
			default:
				return "", nil, invalid("String filters support eq or contains")
			}
			args = append(args, text)
		} else {
			var n float64
			valid := true
			switch v := f.Value.(type) {
			case float64:
				n = v
			case int:
				n = float64(v)
			case int64:
				n = float64(v)
			case json.Number:
				var e error
				n, e = strconv.ParseFloat(string(v), 64)
				valid = e == nil
			default:
				valid = false
			}
			if !valid || math.IsNaN(n) || math.IsInf(n, 0) {
				return "", nil, invalid("Attribute filter value must be a string or finite number")
			}
			op := map[string]string{"eq": "=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[f.Op]
			if op == "" {
				return "", nil, invalid("Numeric filters support eq, gt, gte, lt or lte")
			}
			// Strings must themselves encode a complete JSON number, not prose or units.
			clause += `CASE WHEN json_valid(af.value) THEN json_type(af.value) ELSE '' END IN ('integer','real') AND ABS(CAST(af.value AS REAL))<=1.7976931348623157e308 AND CAST(af.value AS REAL) ` + op + ` ?`
			args = append(args, n)
		}
		query += clause + ")"
	}
	return query, args, nil
}
