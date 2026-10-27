package exprvalue

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/conf"
	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/parser/lexer"
)

// Compile validates the source dialect before any optimizer can transform it.
//
//nolint:nonamedreturns // Panic recovery replaces both returned values at the compiler boundary.
func Compile(source string) (program *Program, err error) {
	defer func() {
		if recover() != nil {
			program = nil
			err = fault(evaluationCode)
		}
	}()
	if len(source) > sourceBytes {
		return nil, fault("expression_limit")
	}
	if !utf8.ValidString(source) || strings.TrimSpace(source) == "" {
		return nil, fault("expression_value_invalid")
	}
	offsets := []int{}
	for index := range source {
		offsets = append(offsets, index)
	}
	offsets = append(offsets, len(source))
	policy := sourcePolicy{offsets: offsets, roots: map[string]bool{}, steps: map[string]bool{}}
	err = policy.checkTokens(source)
	if err != nil {
		return nil, err
	}
	config := conf.CreateNew()
	config.MaxNodes = sourceNodes
	tree, err := parser.ParseWithConfig(source, config)
	if err != nil {
		return nil, policy.compileError(err)
	}
	err = policy.check(tree.Node, 1, 0)
	if err != nil {
		return nil, err
	}
	patch := rewrite{offsets: offsets}
	// The visitor registers private per-site closures, preserving safe spans without
	// exposing metadata arguments or callable values in the author environment.
	options := []expr.Option{
		expr.Env(Environment{}),
		expr.Optimize(false),
		expr.DisableAllBuiltins(),
		expr.EnableBuiltin(
			"all",
		),
		expr.EnableBuiltin("filter"),
		expr.EnableBuiltin("map"),
		expr.MaxNodes(compileParseNodes),
	}
	// Build the private function inventory using the same post-order walk as Patch.
	// Patch receives the already planned visitor on a fresh source tree; no generated
	// node is ever walked or rewritten recursively.
	ast.Walk(&tree.Node, &patch)
	for _, registration := range patch.functions {
		options = append(options, expr.Function(registration.name, registration.fn))
	}
	options = append(options, expr.Patch(&rewrite{offsets: offsets}))
	code, err := expr.Compile(source, options...)
	if err != nil {
		return nil, policy.compileError(err)
	}
	return &Program{code: code, offsets: offsets, roots: sortedKeys(policy.roots), steps: sortedKeys(policy.steps)}, nil
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func byteSpan(offsets []int, loc file.Location) (int, int) {
	start := max(0, min(loc.From, len(offsets)-1))
	end := max(start, min(loc.To, len(offsets)-1))
	return offsets[start], offsets[end]
}

type sourcePolicy struct {
	nodes        int
	offsets      []int
	roots, steps map[string]bool
}

func (p *sourcePolicy) failure(code string, location file.Location) error {
	start, end := byteSpan(p.offsets, location)
	return &Failure{Code: code, Start: start, End: end}
}

func (p *sourcePolicy) compileError(err error) error {
	if located, ok := errors.AsType[*file.Error](err); ok {
		code := "expression_type"
		if located.Message == "compilation failed: expression exceeds maximum allowed nodes" {
			code = "expression_limit"
		}
		return p.failure(code, located.Location)
	}
	return fault("expression_type")
}

func (p *sourcePolicy) checkTokens(source string) error {
	tokens, err := lexer.Lex(file.NewSource(source))
	if err != nil {
		return p.compileError(err)
	}
	for _, token := range tokens {
		if token.Is(lexer.Operator, "::") {
			return p.failure("expression_type", token.Location)
		}
		if !token.Is(lexer.Number) {
			continue
		}
		text := strings.ReplaceAll(strings.ToLower(token.Value), "_", "")
		if strings.HasPrefix(text, "0x") || !strings.ContainsAny(text, ".e") {
			continue
		}
		number, parseErr := strconv.ParseFloat(text, 64)
		mantissa, _, _ := strings.Cut(text, "e")
		if parseErr != nil || math.IsInf(number, 0) || (number == 0 && strings.Trim(mantissa, "0.") != "") {
			return p.failure("expression_value_invalid", token.Location)
		}
	}
	return nil
}

func collection(name string) bool { return name == "all" || name == "filter" || name == "map" }

//nolint:gocognit,gocyclo,cyclop,funlen // One closed AST dispatch makes forbidden syntax and source-child accounting auditable together.
func (p *sourcePolicy) check(node ast.Node, depth, predicates int) error {
	p.nodes++
	if p.nodes > sourceNodes || depth > sourceDepth || predicates > predicateDepth {
		return p.failure("expression_limit", node.Location())
	}
	var children []ast.Node
	switch n := node.(type) {
	case *ast.NilNode, *ast.BoolNode, *ast.StringNode, *ast.IntegerNode:
	case *ast.FloatNode:
		if math.IsInf(n.Value, 0) || math.IsNaN(n.Value) {
			return p.failure("expression_value_invalid", n.Location())
		}
	case *ast.IdentifierNode:
		if n.Value == nullToken {
			break
		}
		if n.Value != "run" && n.Value != "trigger" && n.Value != "vars" && n.Value != "steps" {
			return p.failure("expression_type", n.Location())
		}
		p.roots[n.Value] = true
	case *ast.UnaryNode:
		if n.Operator != "+" && n.Operator != "-" && n.Operator != "!" && n.Operator != "not" {
			return p.failure("expression_type", n.Location())
		}
		children = []ast.Node{n.Node}
	case *ast.BinaryNode:
		switch n.Operator {
		case "+",
			"-",
			"*",
			"/",
			"%",
			"==",
			"!=",
			"<",
			"<=",
			">",
			">=",
			"&&",
			"||",
			"and",
			"or",
			"??",
			"in",
			"not in",
			"contains",
			"startsWith",
			"endsWith":
		default:
			return p.failure("expression_type", n.Location())
		}
		children = []ast.Node{n.Left, n.Right}
	case *ast.MemberNode:
		if n.Optional || n.Method {
			return p.failure("expression_type", n.Location())
		}
		if root, ok := n.Node.(*ast.IdentifierNode); ok && root.Value == "steps" {
			if key, isString := n.Property.(*ast.StringNode); isString {
				p.steps[key.Value] = true
			}
		}
		children = []ast.Node{n.Node, n.Property}
	case *ast.CallNode:
		callee, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			return p.failure("expression_type", n.Location())
		}
		low, high := helperBounds(callee.Value)
		if high == 0 || len(n.Arguments) < low || len(n.Arguments) > high {
			return p.failure("expression_type", n.Location())
		}
		// Count the callee as a source node, without mistaking it for a root.
		p.nodes++
		if p.nodes > sourceNodes || depth+1 > sourceDepth {
			return p.failure("expression_limit", n.Location())
		}
		children = n.Arguments
	case *ast.BuiltinNode:
		if collection(n.Name) || (n.Name == helperSum && len(n.Arguments) == predicateArguments) {
			if len(n.Arguments) != predicateArguments {
				return p.failure("expression_type", n.Location())
			}
			predicates++
			if predicates > predicateDepth {
				return p.failure("expression_limit", n.Location())
			}
		} else {
			low, high := helperBounds(n.Name)
			if high == 0 || len(n.Arguments) < low || len(n.Arguments) > high {
				return p.failure("expression_type", n.Location())
			}
		}
		children = n.Arguments
	case *ast.PredicateNode:
		if predicates == 0 {
			return p.failure("expression_type", n.Location())
		}
		children = []ast.Node{n.Node}
	case *ast.PointerNode:
		if predicates == 0 || (n.Name != "" && n.Name != "index") {
			return p.failure("expression_type", n.Location())
		}
	case *ast.ConditionalNode:
		children = []ast.Node{n.Cond, n.Exp1, n.Exp2}
	case *ast.ArrayNode:
		if len(n.Nodes) > containerSize {
			return p.failure("expression_limit", n.Location())
		}
		children = n.Nodes
	case *ast.MapNode:
		if len(n.Pairs) > containerSize {
			return p.failure("expression_limit", n.Location())
		}
		children = n.Pairs
	case *ast.PairNode:
		if _, ok := n.Key.(*ast.StringNode); !ok {
			return p.failure("expression_type", n.Location())
		}
		children = []ast.Node{n.Key, n.Value}
	default:
		return p.failure("expression_type", node.Location())
	}
	for _, child := range children {
		if err := p.check(child, depth+1, predicates); err != nil {
			return err
		}
	}
	return nil
}

type registration struct {
	name string
	fn   helper
}
type rewrite struct {
	offsets   []int
	functions []registration
}

//nolint:nonamedreturns // Named closure results are required for boundary panic recovery.
func (r *rewrite) call(site ast.Node, fn helper, arguments ...ast.Node) ast.Node {
	name := "__hearth_" + strconv.Itoa(len(r.functions))
	start, end := byteSpan(r.offsets, site.Location())
	wrapped := func(args ...any) (result any, err error) {
		defer func() {
			if recover() != nil {
				result = nil
				err = &Failure{Code: evaluationCode, Start: start, End: end}
			}
		}()
		result, err = fn(args...)
		if err != nil {
			if failure, ok := errors.AsType[*Failure](err); ok {
				return nil, &Failure{Code: failure.Code, Start: start, End: end}
			}
			return nil, &Failure{Code: evaluationCode, Start: start, End: end}
		}
		return result, nil
	}
	r.functions = append(r.functions, registration{name: name, fn: wrapped})
	return &ast.CallNode{Callee: &ast.IdentifierNode{Value: name}, Arguments: arguments}
}

//nolint:gocognit // This mirrors the closed source-node families; generated nodes are never revisited.
func (r *rewrite) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		if n.Value == nullToken {
			ast.Patch(node, &ast.NilNode{})
		}
	case *ast.IntegerNode:
		ast.Patch(node, &ast.ConstantNode{Value: int64(n.Value)})
	case *ast.PointerNode:
		if n.Name == "index" {
			ast.Patch(node, r.call(n, func(args ...any) (any, error) {
				index, ok := args[0].(int)
				if !ok {
					return nil, fault("expression_type")
				}
				return int64(index), nil
			}, n))
		}
	case *ast.MemberNode:
		ast.Patch(node, r.call(n, required, n.Node, n.Property))
	case *ast.BinaryNode:
		if n.Operator == "+" {
			ast.Patch(node, r.call(n, add, n.Left, n.Right))
		}
		if n.Operator == "-" || n.Operator == "*" || n.Operator == "/" || n.Operator == "%" {
			ast.Patch(node, r.call(n, guard, n))
		}
	case *ast.UnaryNode:
		if n.Operator == "+" || n.Operator == "-" {
			ast.Patch(node, r.call(n, guard, n))
		}
	case *ast.ArrayNode, *ast.MapNode:
		ast.Patch(node, r.call(n, guard, n))
	case *ast.PredicateNode:
		body := r.call(n.Node, guard, n.Node)
		body.SetLocation(n.Node.Location())
		n.Node = body
	case *ast.BuiltinNode:
		if collection(n.Name) {
			n.Arguments[0] = r.call(n.Arguments[0], collectionInput, n.Arguments[0])
			ast.Patch(node, r.call(n, guard, n))
			return
		}
		name := n.Name
		if name == helperSum && len(n.Arguments) == predicateArguments {
			input := r.call(n.Arguments[0], collectionInput, n.Arguments[0])
			mapped := &ast.BuiltinNode{Name: "map", Arguments: []ast.Node{input, n.Arguments[1]}}
			mapped.SetLocation(n.Location())
			bounded := r.call(n, guard, mapped)
			ast.Patch(
				node,
				r.call(n, func(args ...any) (any, error) { return callHelper(helperSum, args...) }, bounded),
			)
			return
		}
		ast.Patch(node, r.call(n, func(args ...any) (any, error) { return callHelper(name, args...) }, n.Arguments...))
	case *ast.CallNode:
		// Source policy established a fixed helper identifier before rewriting.
		callee, _ := n.Callee.(*ast.IdentifierNode)
		name := callee.Value
		ast.Patch(node, r.call(n, func(args ...any) (any, error) { return callHelper(name, args...) }, n.Arguments...))
	}
}
