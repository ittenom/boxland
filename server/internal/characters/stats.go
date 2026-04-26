// Boxland — characters: stat definitions + formula evaluator + point-buy.
//
// A stat set is a designer-authored list of StatDef entries. Each entry
// declares a kind (core/derived/resource/hidden), display order, default
// value, and — for derived stats — a Formula string the evaluator
// computes against the player's allocations + other stat values.
//
// The formula evaluator is deliberately small. Grammar:
//
//   expr   := term (('+' | '-') term)*
//   term   := factor (('*' | '/') factor)*
//   factor := number | ident | '(' expr ')' | call
//   call   := ('clamp' | 'min' | 'max') '(' expr ',' expr (',' expr)? ')'
//
// All arithmetic is integer (Go int). `/` truncates toward zero. The
// only callable identifiers are clamp/min/max; bare identifiers must
// resolve to a stat key in the supplied scope. Errors include 1-based
// column numbers so the designer-side editor can underline the offending
// position.
//
// No code execution: the evaluator never reads the filesystem, network,
// or any global state. Pure function of (formula, scope).

package characters

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Stat definition shapes
// ---------------------------------------------------------------------------

// StatDef is one entry in a StatSet's stats_json array.
type StatDef struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Kind         StatKind `json:"kind"`
	Default      int      `json:"default"`
	Min          int      `json:"min"`
	Max          int      `json:"max"`
	CreationCost int      `json:"creation_cost"` // points spent per +1 in point-buy
	DisplayOrder int      `json:"display_order"`
	Formula      string   `json:"formula,omitempty"` // empty for non-derived
	Cap          *int     `json:"cap,omitempty"`     // optional max-after-formula
}

// Validate enforces structural invariants on one StatDef.
func (d StatDef) Validate() error {
	if err := validateSlotKey(d.Key); err != nil {
		return fmt.Errorf("stat %q: %w", d.Key, err)
	}
	if strings.TrimSpace(d.Label) == "" {
		return fmt.Errorf("stat %q: label is required", d.Key)
	}
	if err := d.Kind.Validate(); err != nil {
		return fmt.Errorf("stat %q: %w", d.Key, err)
	}
	if d.Min > d.Max && d.Max != 0 {
		// Max=0 is a legal "no upper bound" sentinel.
		return fmt.Errorf("stat %q: min (%d) > max (%d)", d.Key, d.Min, d.Max)
	}
	if d.CreationCost < 0 {
		return fmt.Errorf("stat %q: creation_cost must be non-negative", d.Key)
	}
	switch d.Kind {
	case StatDerived:
		if strings.TrimSpace(d.Formula) == "" {
			return fmt.Errorf("stat %q: derived stats require a formula", d.Key)
		}
	default:
		// core/resource/hidden may not carry a formula — keeps the
		// designer's intent unambiguous (a "core" stat with a formula
		// is almost certainly a mis-classified derived stat).
		if strings.TrimSpace(d.Formula) != "" {
			return fmt.Errorf("stat %q: only derived stats may have a formula", d.Key)
		}
	}
	return nil
}

// CreationRules drives the point-buy validator.
type CreationRules struct {
	// Method picks the creation flow. "fixed" uses every stat's
	// Default verbatim. "point_buy" lets the player spend Pool points
	// against creation_cost. "freeform" lets a designer set arbitrary
	// values inside [Min, Max] without budget. Player mode (Phase 4)
	// will only allow point_buy by default.
	Method string `json:"method"` // fixed | point_buy | freeform
	Pool   int    `json:"pool,omitempty"`
}

// Validate enforces structural invariants on a CreationRules.
func (r CreationRules) Validate() error {
	switch r.Method {
	case "", "fixed", "point_buy", "freeform":
	default:
		return fmt.Errorf("creation_rules: method %q is not one of fixed|point_buy|freeform", r.Method)
	}
	if r.Method == "point_buy" && r.Pool < 0 {
		return fmt.Errorf("creation_rules: point_buy pool must be non-negative")
	}
	return nil
}

// ParsedStatSet is the typed view of a stored StatSet row's JSON
// columns. Designers edit these via the Generator + the stat-set CRUD
// surface (Phase 3 lands the reader; the editor UI follows).
type ParsedStatSet struct {
	Defs  []StatDef
	Rules CreationRules
}

// ParseStatSet decodes a row's stats_json + creation_rules_json into
// the typed shape. Returns a wrapped error keyed by which column was
// malformed — keeps the designer-side error chips precise.
func ParseStatSet(row StatSet) (ParsedStatSet, error) {
	var out ParsedStatSet
	if len(row.StatsJSON) > 0 {
		if err := json.Unmarshal(row.StatsJSON, &out.Defs); err != nil {
			return out, fmt.Errorf("stats_json: %w", err)
		}
	}
	if len(row.CreationRulesJSON) > 0 {
		if err := json.Unmarshal(row.CreationRulesJSON, &out.Rules); err != nil {
			return out, fmt.Errorf("creation_rules_json: %w", err)
		}
	}
	// Validate each def + the rules. Surfaces designer mistakes early.
	keys := make(map[string]struct{}, len(out.Defs))
	for _, d := range out.Defs {
		if err := d.Validate(); err != nil {
			return out, err
		}
		if _, dup := keys[d.Key]; dup {
			return out, fmt.Errorf("stat key %q appears more than once", d.Key)
		}
		keys[d.Key] = struct{}{}
	}
	if err := out.Rules.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Point-buy validation
// ---------------------------------------------------------------------------

// ValidateAllocation enforces the point-buy rules of a stat set against
// a given allocation map. Returns nil if the allocation is legal.
//
// Rules (per spec §Stats model):
//   - Each core stat's (default + alloc[key]) must lie within [Min, Max].
//   - Sum of (alloc[key] * StatDef.CreationCost) for core stats must
//     equal the rules.Pool when method == "point_buy".
//   - "fixed" rejects any non-zero allocation.
//   - "freeform" allows any allocation as long as the per-stat range
//     constraint holds.
//
// Allocations targeting unknown stat keys, derived stats, or
// resource/hidden stats are an error.
func ValidateAllocation(set ParsedStatSet, allocations map[string]int) error {
	defByKey := make(map[string]StatDef, len(set.Defs))
	for _, d := range set.Defs {
		defByKey[d.Key] = d
	}

	if set.Rules.Method == "fixed" {
		for k, v := range allocations {
			if v != 0 {
				return fmt.Errorf("stat %q: fixed creation rejects allocation (got %d)", k, v)
			}
		}
		return nil
	}

	pool := 0
	for k, v := range allocations {
		def, ok := defByKey[k]
		if !ok {
			return fmt.Errorf("allocation: unknown stat key %q", k)
		}
		if def.Kind != StatCore {
			return fmt.Errorf("allocation: stat %q is %s, only core stats accept allocations", k, def.Kind)
		}
		final := def.Default + v
		if final < def.Min {
			return fmt.Errorf("stat %q: allocated %d -> total %d below min %d", k, v, final, def.Min)
		}
		if def.Max != 0 && final > def.Max {
			return fmt.Errorf("stat %q: allocated %d -> total %d above max %d", k, v, final, def.Max)
		}
		if def.CreationCost > 0 && v > 0 {
			pool += v * def.CreationCost
		}
		// Refunds (negative allocation) are NOT credited back into the
		// pool — point-buy systems traditionally make the spend
		// one-way, otherwise designers can't reason about budgets.
	}
	if set.Rules.Method == "point_buy" && pool != set.Rules.Pool {
		return fmt.Errorf("allocation: spent %d points, pool requires %d", pool, set.Rules.Pool)
	}
	return nil
}

// ResolveStats computes the final value of every stat for a recipe.
// Core values = default + allocation; derived values = Eval(formula,
// scope) clamped by Cap if set. Resource/hidden stats default to their
// Default value (their semantics are runtime; no creation-time spend).
//
// Returns the result map keyed by stat key. The map iteration order is
// undefined; UI layers should sort by StatDef.DisplayOrder.
func ResolveStats(set ParsedStatSet, allocations map[string]int) (map[string]int, error) {
	if err := ValidateAllocation(set, allocations); err != nil {
		return nil, err
	}

	scope := make(map[string]int, len(set.Defs))
	// First pass: core/resource/hidden values.
	for _, d := range set.Defs {
		switch d.Kind {
		case StatCore:
			scope[d.Key] = d.Default + allocations[d.Key]
		case StatResource, StatHidden:
			scope[d.Key] = d.Default
		}
	}
	// Second pass: derived stats. We loop until no value changes (so
	// derived-of-derived works in arbitrary order) or until N passes
	// pass without progress (cycle detection).
	derivedDefs := make([]StatDef, 0, len(set.Defs))
	for _, d := range set.Defs {
		if d.Kind == StatDerived {
			derivedDefs = append(derivedDefs, d)
		}
	}
	resolved := make(map[string]struct{}, len(derivedDefs))
	for pass := 0; pass < len(derivedDefs)+1; pass++ {
		progress := false
		for _, d := range derivedDefs {
			if _, done := resolved[d.Key]; done {
				continue
			}
			val, err := EvalFormula(d.Formula, scope)
			if err != nil {
				// Soft error: missing dependency. Try again next pass.
				if errors.Is(err, errMissingIdent) {
					continue
				}
				return nil, fmt.Errorf("derived stat %q: %w", d.Key, err)
			}
			if d.Cap != nil && val > *d.Cap {
				val = *d.Cap
			}
			scope[d.Key] = val
			resolved[d.Key] = struct{}{}
			progress = true
		}
		if !progress {
			break
		}
	}
	for _, d := range derivedDefs {
		if _, ok := resolved[d.Key]; !ok {
			return nil, fmt.Errorf("derived stat %q: cannot resolve formula (missing dependency or cycle)", d.Key)
		}
	}
	return scope, nil
}

// SortedStatDefs returns set.Defs sorted by DisplayOrder, then Key. Used
// by the generator UI's stat allocator panel for stable ordering.
func SortedStatDefs(set ParsedStatSet) []StatDef {
	out := make([]StatDef, len(set.Defs))
	copy(out, set.Defs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DisplayOrder != out[j].DisplayOrder {
			return out[i].DisplayOrder < out[j].DisplayOrder
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// ---------------------------------------------------------------------------
// Formula evaluator
// ---------------------------------------------------------------------------

// errMissingIdent is a sentinel returned when an identifier doesn't
// resolve to a value in the scope. ResolveStats uses this to defer
// evaluation across passes (derived-of-derived).
var errMissingIdent = errors.New("formula: missing identifier")

// EvalFormula parses and evaluates `formula` against the supplied
// scope. Pure function. See package doc for the supported grammar.
func EvalFormula(formula string, scope map[string]int) (int, error) {
	p := newFormulaParser(formula)
	if p.tokenErr != nil {
		return 0, p.tokenErr
	}
	val, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	if p.peek().kind != tkEOF {
		return 0, p.errf("unexpected trailing token %q", p.peek().lexeme)
	}
	return val.eval(scope)
}

// ---- Tokenizer -----------------------------------------------------------

type tokenKind int

const (
	tkEOF tokenKind = iota
	tkNumber
	tkIdent
	tkPlus
	tkMinus
	tkStar
	tkSlash
	tkLParen
	tkRParen
	tkComma
)

type token struct {
	kind   tokenKind
	lexeme string
	value  int
	col    int // 1-based column number in the source
}

type tokens struct {
	src    string
	pos    int
	tokens []token
	idx    int
}

func tokenize(src string) (*tokens, error) {
	t := &tokens{src: src}
	col := 1
	for t.pos < len(src) {
		c := src[t.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			t.pos++
			col++
		case c == '+':
			t.tokens = append(t.tokens, token{kind: tkPlus, lexeme: "+", col: col})
			t.pos++
			col++
		case c == '-':
			t.tokens = append(t.tokens, token{kind: tkMinus, lexeme: "-", col: col})
			t.pos++
			col++
		case c == '*':
			t.tokens = append(t.tokens, token{kind: tkStar, lexeme: "*", col: col})
			t.pos++
			col++
		case c == '/':
			t.tokens = append(t.tokens, token{kind: tkSlash, lexeme: "/", col: col})
			t.pos++
			col++
		case c == '(':
			t.tokens = append(t.tokens, token{kind: tkLParen, lexeme: "(", col: col})
			t.pos++
			col++
		case c == ')':
			t.tokens = append(t.tokens, token{kind: tkRParen, lexeme: ")", col: col})
			t.pos++
			col++
		case c == ',':
			t.tokens = append(t.tokens, token{kind: tkComma, lexeme: ",", col: col})
			t.pos++
			col++
		case c >= '0' && c <= '9':
			start := t.pos
			startCol := col
			for t.pos < len(src) && src[t.pos] >= '0' && src[t.pos] <= '9' {
				t.pos++
				col++
			}
			lex := src[start:t.pos]
			n, err := atoi(lex)
			if err != nil {
				return nil, fmt.Errorf("formula: at col %d: %w", startCol, err)
			}
			t.tokens = append(t.tokens, token{kind: tkNumber, lexeme: lex, value: n, col: startCol})
		case isIdentStart(c):
			start := t.pos
			startCol := col
			for t.pos < len(src) && isIdentBody(src[t.pos]) {
				t.pos++
				col++
			}
			t.tokens = append(t.tokens, token{kind: tkIdent, lexeme: src[start:t.pos], col: startCol})
		default:
			return nil, fmt.Errorf("formula: at col %d: unexpected character %q", col, string(c))
		}
	}
	t.tokens = append(t.tokens, token{kind: tkEOF, col: col})
	return t, nil
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentBody(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func atoi(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty number")
	}
	n := 0
	for i := 0; i < len(s); i++ {
		d := int(s[i] - '0')
		// Overflow guard: int is 64-bit on every supported platform.
		if n > (1<<62)/10 {
			return 0, fmt.Errorf("number %q overflows", s)
		}
		n = n*10 + d
	}
	return n, nil
}

// ---- Parser --------------------------------------------------------------

type formulaParser struct {
	t        *tokens
	tokenErr error // stashed tokenizer error; surfaces on first parseExpr call
}

func newFormulaParser(src string) *formulaParser {
	t, err := tokenize(src)
	if err != nil {
		// Defer the error to the first call against the parser; the
		// public API stays a single Eval entry point. Stash the actual
		// tokenizer error so parseExpr returns the helpful message
		// (column + bad character) instead of a generic "unexpected end".
		return &formulaParser{t: &tokens{tokens: []token{{kind: tkEOF}}}, tokenErr: err}
	}
	return &formulaParser{t: t}
}

func (p *formulaParser) peek() token {
	if p.t.idx >= len(p.t.tokens) {
		return token{kind: tkEOF}
	}
	return p.t.tokens[p.t.idx]
}

func (p *formulaParser) advance() token {
	t := p.peek()
	p.t.idx++
	return t
}

func (p *formulaParser) errf(format string, args ...any) error {
	return fmt.Errorf("formula: at col %d: %s", p.peek().col, fmt.Sprintf(format, args...))
}

// node is the AST shape. We keep it as a discriminated struct so
// evaluation is a single switch — small enough that the indirection
// cost vs. interfaces is preferable for clarity.
type node struct {
	op       byte // '+' '-' '*' '/' or 0 for leaves
	number   int
	ident    string
	call     string // "clamp" "min" "max" or ""
	children []*node
}

func (n *node) eval(scope map[string]int) (int, error) {
	if n == nil {
		return 0, errors.New("formula: nil node")
	}
	if n.call != "" {
		args := make([]int, len(n.children))
		for i, c := range n.children {
			v, err := c.eval(scope)
			if err != nil {
				return 0, err
			}
			args[i] = v
		}
		switch n.call {
		case "clamp":
			if len(args) != 3 {
				return 0, errors.New("formula: clamp(x, lo, hi) takes 3 arguments")
			}
			x, lo, hi := args[0], args[1], args[2]
			if lo > hi {
				return 0, errors.New("formula: clamp lo > hi")
			}
			if x < lo {
				return lo, nil
			}
			if x > hi {
				return hi, nil
			}
			return x, nil
		case "min":
			if len(args) < 2 {
				return 0, errors.New("formula: min takes at least 2 arguments")
			}
			out := args[0]
			for _, v := range args[1:] {
				if v < out {
					out = v
				}
			}
			return out, nil
		case "max":
			if len(args) < 2 {
				return 0, errors.New("formula: max takes at least 2 arguments")
			}
			out := args[0]
			for _, v := range args[1:] {
				if v > out {
					out = v
				}
			}
			return out, nil
		default:
			return 0, fmt.Errorf("formula: unknown call %q", n.call)
		}
	}
	if n.ident != "" {
		v, ok := scope[n.ident]
		if !ok {
			return 0, fmt.Errorf("%w: %q", errMissingIdent, n.ident)
		}
		return v, nil
	}
	if n.op == 0 {
		return n.number, nil
	}
	if len(n.children) != 2 {
		return 0, fmt.Errorf("formula: binary node has %d children", len(n.children))
	}
	l, err := n.children[0].eval(scope)
	if err != nil {
		return 0, err
	}
	r, err := n.children[1].eval(scope)
	if err != nil {
		return 0, err
	}
	switch n.op {
	case '+':
		return l + r, nil
	case '-':
		return l - r, nil
	case '*':
		return l * r, nil
	case '/':
		if r == 0 {
			return 0, errors.New("formula: division by zero")
		}
		return l / r, nil
	}
	return 0, fmt.Errorf("formula: unknown op %q", string(n.op))
}

func (p *formulaParser) parseExpr() (*node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().kind {
		case tkPlus, tkMinus:
			op := p.advance()
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = &node{op: op.lexeme[0], children: []*node{left, right}}
		default:
			return left, nil
		}
	}
}

func (p *formulaParser) parseTerm() (*node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().kind {
		case tkStar, tkSlash:
			op := p.advance()
			right, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			left = &node{op: op.lexeme[0], children: []*node{left, right}}
		default:
			return left, nil
		}
	}
}

func (p *formulaParser) parseFactor() (*node, error) {
	tok := p.peek()
	switch tok.kind {
	case tkNumber:
		p.advance()
		return &node{number: tok.value}, nil
	case tkMinus:
		// Unary minus: parse as 0 - factor.
		p.advance()
		inner, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &node{op: '-', children: []*node{{number: 0}, inner}}, nil
	case tkPlus:
		// Unary plus: no-op.
		p.advance()
		return p.parseFactor()
	case tkLParen:
		p.advance()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tkRParen {
			return nil, p.errf("expected ')'")
		}
		p.advance()
		return inner, nil
	case tkIdent:
		// Function call vs bare identifier.
		p.advance()
		switch tok.lexeme {
		case "clamp", "min", "max":
			if p.peek().kind != tkLParen {
				return nil, p.errf("expected '(' after %s", tok.lexeme)
			}
			p.advance()
			args, err := p.parseCallArgs()
			if err != nil {
				return nil, err
			}
			return &node{call: tok.lexeme, children: args}, nil
		}
		return &node{ident: tok.lexeme}, nil
	case tkEOF:
		return nil, p.errf("unexpected end of formula")
	default:
		return nil, p.errf("unexpected token %q", tok.lexeme)
	}
}

func (p *formulaParser) parseCallArgs() ([]*node, error) {
	if p.peek().kind == tkRParen {
		// Reject empty call — clamp/min/max all require >=2 args.
		return nil, p.errf("call needs arguments")
	}
	args := []*node{}
	for {
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		switch p.peek().kind {
		case tkComma:
			p.advance()
		case tkRParen:
			p.advance()
			return args, nil
		default:
			return nil, p.errf("expected ',' or ')' in call")
		}
	}
}
