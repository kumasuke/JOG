package s3select

import "fmt"

// match reports whether the record satisfies the WHERE expression. A nil
// expression matches everything.
func match(e Expr, rec Record) (bool, error) {
	if e == nil {
		return true, nil
	}
	switch x := e.(type) {
	case *boolExpr:
		l, err := match(x.left, rec)
		if err != nil {
			return false, err
		}
		switch x.op {
		case opAnd:
			if !l {
				return false, nil
			}
			return match(x.right, rec)
		case opOr:
			if l {
				return true, nil
			}
			return match(x.right, rec)
		}
		return false, fmt.Errorf("%w: unknown boolean operator", ErrParse)
	case *notExpr:
		v, err := match(x.inner, rec)
		if err != nil {
			return false, err
		}
		return !v, nil
	case *comparison:
		return evalComparison(x, rec)
	default:
		return false, fmt.Errorf("%w: unknown expression node", ErrParse)
	}
}

// evalComparison applies the weakly-typed S3 Select comparison: if both operands
// parse as numbers the comparison is numeric, otherwise it is string-based. A
// missing column reference makes the comparison false.
func evalComparison(c *comparison, rec Record) (bool, error) {
	lv, lok := resolveOperand(c.left, rec)
	rv, rok := resolveOperand(c.right, rec)
	if !lok || !rok {
		return false, nil
	}

	ln, lIsNum := asNumber(lv)
	rn, rIsNum := asNumber(rv)
	if lIsNum && rIsNum {
		return compareNum(ln, rn, c.op), nil
	}
	return compareStr(lv, rv, c.op), nil
}

// resolveOperand returns the operand's value. For a literal it is the literal
// text; for a column reference it is the column's value (ok=false when absent).
func resolveOperand(o operand, rec Record) (string, bool) {
	if o.isLiteral {
		return o.literal, true
	}
	if o.col == nil {
		return "", false
	}
	return resolveColumn(*o.col, rec)
}

// resolveColumn looks up a column by position (_N) or by name.
func resolveColumn(ref ColumnRef, rec Record) (string, bool) {
	if ref.Position >= 1 {
		return rec.ByPosition(ref.Position)
	}
	return rec.ByName(ref.Name)
}

func compareNum(a, b float64, op cmpOp) bool {
	switch op {
	case cmpEq:
		return a == b
	case cmpNe:
		return a != b
	case cmpLt:
		return a < b
	case cmpLe:
		return a <= b
	case cmpGt:
		return a > b
	case cmpGe:
		return a >= b
	}
	return false
}

func compareStr(a, b string, op cmpOp) bool {
	switch op {
	case cmpEq:
		return a == b
	case cmpNe:
		return a != b
	case cmpLt:
		return a < b
	case cmpLe:
		return a <= b
	case cmpGt:
		return a > b
	case cmpGe:
		return a >= b
	}
	return false
}

// project applies the SELECT projection to a record, returning the output
// values in order. An empty projection (SELECT *) returns all values.
func project(items []SelectItem, rec Record) ([]string, error) {
	if len(items) == 0 {
		return rec.Values, nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		v, _ := resolveColumn(it.Ref, rec)
		// Missing columns project as empty, matching S3's lenient behaviour.
		out = append(out, v)
	}
	return out, nil
}

// projectNames returns the output column names for the projection, used by the
// JSON output writer. For SELECT * it returns the record's own names. For an
// explicit projection it returns each referenced name (or _N for positional
// refs) so JSON objects keep meaningful keys.
func projectNames(items []SelectItem, rec Record) []string {
	if len(items) == 0 {
		return rec.Names
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		if it.Ref.Position >= 1 {
			names = append(names, fmt.Sprintf("_%d", it.Ref.Position))
		} else {
			names = append(names, it.Ref.Name)
		}
	}
	return names
}
