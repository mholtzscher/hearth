package exprvalue

import (
	"math"
	"strconv"
	"strings"
)

type helper func(...any) (any, error)

func helperBounds(name string) (int, int) {
	switch name {
	case "len", "round", "string", helperInt, helperFloat:
		return 1, 1
	case helperMin, helperMax:
		return 1, containerSize
	case "join", helperSum:
		return 1, predicateArguments
	case "get":
		return getArguments, getArguments
	case helperHas:
		return predicateArguments, predicateArguments
	default:
		return 0, 0
	}
}

func lookup(container, key any) (any, bool, error) {
	switch c := container.(type) {
	case nil:
		switch k := key.(type) {
		case string:
			return nil, false, nil
		case int64:
			if k >= 0 {
				return nil, false, nil
			}
		}
	case map[string]any:
		if k, ok := key.(string); ok {
			value, found := c[k]
			return value, found, nil
		}
	case []any:
		if k, ok := key.(int64); ok && k >= 0 {
			if k >= int64(len(c)) {
				return nil, false, nil
			}
			return c[k], true, nil
		}
	}
	return nil, false, fault("expression_type")
}

func required(args ...any) (any, error) {
	if args[0] == nil {
		return nil, fault("expression_type")
	}
	value, found, err := lookup(args[0], args[1])
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fault("expression_missing")
	}
	return value, nil
}

func optional(name string, args []any) (any, error) {
	value, found, err := lookup(args[0], args[1])
	if err != nil {
		return nil, err
	}
	if name == helperHas {
		return found, nil
	}
	if !found {
		return args[2], nil
	}
	return value, nil
}

func guard(args ...any) (any, error) {
	state := valueBudget{maxBytes: valueBytes}
	_, err := state.copy(args[0], 1)
	return args[0], err
}

func collectionInput(args ...any) (any, error) {
	if _, ok := args[0].([]any); !ok {
		return nil, fault("expression_type")
	}
	return args[0], nil
}

// add is the one operator wrapper: it bounds strings before concatenation and
// preserves Expr's int64 wraparound and mixed floating-point addition.
func add(args ...any) (any, error) {
	if left, ok := args[0].(string); ok {
		right, isString := args[1].(string)
		if !isString {
			return nil, fault("expression_type")
		}
		return addStrings(left, right)
	}
	left, li, err := numeric(args[0])
	if err != nil {
		return nil, err
	}
	right, ri, err := numeric(args[1])
	if err != nil {
		return nil, err
	}
	if li && ri {
		// numeric's integer flags prove these exact types.
		l, _ := args[0].(int64)
		r, _ := args[1].(int64)
		return l + r, nil
	}
	return finite(left + right)
}

func addStrings(left, right string) (any, error) {
	if len(left) > valueBytes-quoteBytes-len(right) {
		return nil, fault("expression_limit")
	}
	leftSize, err := quotedSize(left)
	if err != nil {
		return nil, err
	}
	rightSize, err := quotedSize(right)
	if err != nil {
		return nil, err
	}
	if leftSize+rightSize-quoteBytes > valueBytes {
		return nil, fault("expression_limit")
	}
	return left + right, nil
}

func numeric(value any) (float64, bool, error) {
	switch n := value.(type) {
	case int64:
		return float64(n), true, nil
	case float64:
		if !math.IsInf(n, 0) && !math.IsNaN(n) {
			return n, false, nil
		}
	}
	return 0, false, fault("expression_type")
}

func finite(n float64) (any, error) {
	if math.IsInf(n, 0) || math.IsNaN(n) {
		return nil, fault("expression_value_invalid")
	}
	return n, nil
}

func callHelper(name string, args ...any) (any, error) {
	if name == "get" || name == helperHas {
		return optional(name, args)
	}
	switch name {
	case "len":
		switch v := args[0].(type) {
		case string:
			return int64(len(v)), nil
		case []any:
			return int64(len(v)), nil
		case map[string]any:
			return int64(len(v)), nil
		}
	case helperInt, helperFloat, "round":
		return convertNumber(name, args[0])
	case "string":
		return scalarString(args[0])
	case "join":
		return join(args)
	case helperSum:
		return sum(args[0])
	case helperMin, helperMax:
		return extreme(name, args)
	}
	return nil, fault("expression_type")
}

func convertNumber(name string, value any) (any, error) {
	n, integer, err := numeric(value)
	if err != nil {
		return nil, err
	}
	switch name {
	case helperInt:
		if integer {
			return value, nil
		}
		n = math.Trunc(n)
		if n < -9223372036854775808.0 || n >= 9223372036854775808.0 {
			return nil, fault("expression_value_invalid")
		}
		return int64(n), nil
	case helperFloat:
		return finite(n)
	default:
		return finite(math.Round(n))
	}
}

func scalarString(value any) (any, error) {
	var result string
	switch v := value.(type) {
	case nil:
		result = nullToken
	case string:
		result = v
	case bool:
		result = strconv.FormatBool(v)
	case int64:
		result = strconv.FormatInt(v, 10)
	case float64:
		result = strconv.FormatFloat(v, 'g', -1, 64)
	default:
		return nil, fault("expression_type")
	}
	return guard(result)
}

func join(args []any) (any, error) {
	array, ok := args[0].([]any)
	if !ok {
		return nil, fault("expression_type")
	}
	separator := ""
	if len(args) == predicateArguments {
		separator, ok = args[1].(string)
		if !ok {
			return nil, fault("expression_type")
		}
	}
	parts := make([]string, len(array))
	// Two quote bytes are shared by the complete result.
	size := quoteBytes
	separatorSize, err := quotedSize(separator)
	if err != nil {
		return nil, err
	}
	for i, item := range array {
		part, isString := item.(string)
		if !isString {
			return nil, fault("expression_type")
		}
		parts[i] = part
		partSize, sizeErr := quotedSize(part)
		if sizeErr != nil {
			return nil, sizeErr
		}
		size += partSize - quoteBytes
		if i > 0 {
			size += separatorSize - quoteBytes
		}
		if size > valueBytes {
			return nil, fault("expression_limit")
		}
	}
	return strings.Join(parts, separator), nil
}

func sum(value any) (any, error) {
	array, ok := value.([]any)
	if !ok {
		return nil, fault("expression_type")
	}
	var total any = int64(0)
	for _, item := range array {
		var err error
		total, err = add(total, item)
		if err != nil {
			return nil, err
		}
	}
	return total, nil
}

func extreme(name string, args []any) (any, error) {
	best := args[0]
	_, _, err := numeric(best)
	if err != nil {
		return nil, err
	}
	for _, item := range args[1:] {
		n, ni, numberErr := numeric(item)
		if numberErr != nil {
			return nil, numberErr
		}
		b, bi, _ := numeric(best)
		less := n < b
		if ni && bi {
			// numeric's integer flags prove these exact types.
			itemInt, _ := item.(int64)
			bestInt, _ := best.(int64)
			less = itemInt < bestInt
		}
		if (name == helperMin && less) || (name == helperMax && !less) {
			best = item
		}
	}
	return best, nil
}
