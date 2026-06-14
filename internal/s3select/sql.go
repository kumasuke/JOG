package s3select

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ErrParse is returned (wrapped) for any SQL the parser cannot handle, whether
// it is malformed or uses a feature outside this implementation's subset. The
// api layer maps it to an S3 InvalidExpression / InvalidArgument 400.
var ErrParse = errors.New("invalid SQL expression")

// Query is the parsed form of a supported S3 Select statement:
//
//	SELECT <projection> FROM S3Object[...] [alias] [WHERE <expr>] [LIMIT n]
type Query struct {
	Projection []SelectItem // empty means SELECT *
	Where      Expr         // nil means no filter
	Limit      *int64       // nil means no limit
}

// SelectItem is one element of an explicit projection list. A SELECT * query is
// represented by an empty Query.Projection rather than a star item.
type SelectItem struct {
	Ref ColumnRef // the referenced column
}

// ColumnRef references a column either positionally (_N, Position>=1) or by name.
type ColumnRef struct {
	Position int    // 1-based; 0 when referenced by name
	Name     string // column name; empty when referenced by position
}

// Expr is a WHERE-clause expression node: *boolExpr, *notExpr, or *comparison.
type Expr interface{ isExpr() }

// boolOp is the connective of a binary boolean expression.
type boolOp int

const (
	opAnd boolOp = iota
	opOr
)

type boolExpr struct {
	op          boolOp
	left, right Expr
}

type notExpr struct{ inner Expr }

// cmpOp is a comparison operator.
type cmpOp int

const (
	cmpEq cmpOp = iota
	cmpNe
	cmpLt
	cmpLe
	cmpGt
	cmpGe
)

// comparison is "<operand> <op> <operand>" where each operand is a column
// reference or a literal.
type comparison struct {
	op          cmpOp
	left, right operand
}

// operand is one side of a comparison.
type operand struct {
	col       *ColumnRef // set when the operand is a column reference
	literal   string     // set when the operand is a literal
	isLiteral bool
}

func (*boolExpr) isExpr()   {}
func (*notExpr) isExpr()    {}
func (*comparison) isExpr() {}

// ----- lexer -----

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tString // single-quoted literal
	tStar
	tComma
	tLParen
	tRParen
	tDot
	tOp // comparison operator
)

type token struct {
	kind tokKind
	val  string
}

type lexer struct {
	src string
	pos int
	tok []token
}

func lex(src string) ([]token, error) {
	l := &lexer{src: src}
	for {
		t, err := l.next()
		if err != nil {
			return nil, err
		}
		l.tok = append(l.tok, t)
		if t.kind == tEOF {
			return l.tok, nil
		}
	}
}

func (l *lexer) next() (token, error) {
	for l.pos < len(l.src) && unicode.IsSpace(rune(l.src[l.pos])) {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{kind: tEOF}, nil
	}
	c := l.src[l.pos]
	switch {
	case c == '*':
		l.pos++
		return token{kind: tStar, val: "*"}, nil
	case c == ',':
		l.pos++
		return token{kind: tComma, val: ","}, nil
	case c == '(':
		l.pos++
		return token{kind: tLParen, val: "("}, nil
	case c == ')':
		l.pos++
		return token{kind: tRParen, val: ")"}, nil
	case c == '.':
		l.pos++
		return token{kind: tDot, val: "."}, nil
	case c == '=':
		l.pos++
		return token{kind: tOp, val: "="}, nil
	case c == '<':
		l.pos++
		if l.pos < len(l.src) && (l.src[l.pos] == '=' || l.src[l.pos] == '>') {
			op := "<" + string(l.src[l.pos])
			l.pos++
			return token{kind: tOp, val: op}, nil
		}
		return token{kind: tOp, val: "<"}, nil
	case c == '>':
		l.pos++
		if l.pos < len(l.src) && l.src[l.pos] == '=' {
			l.pos++
			return token{kind: tOp, val: ">="}, nil
		}
		return token{kind: tOp, val: ">"}, nil
	case c == '!':
		l.pos++
		if l.pos < len(l.src) && l.src[l.pos] == '=' {
			l.pos++
			return token{kind: tOp, val: "!="}, nil
		}
		return token{}, fmt.Errorf("%w: unexpected '!'", ErrParse)
	case c == '\'':
		return l.lexString()
	case c == '"':
		return l.lexQuotedIdent()
	case c >= '0' && c <= '9':
		return l.lexNumber()
	case c == '_' || unicode.IsLetter(rune(c)):
		return l.lexIdent()
	default:
		return token{}, fmt.Errorf("%w: unexpected character %q", ErrParse, string(c))
	}
}

func (l *lexer) lexString() (token, error) {
	l.pos++ // opening quote
	var sb strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '\'' {
			// '' is an escaped single quote.
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '\'' {
				sb.WriteByte('\'')
				l.pos += 2
				continue
			}
			l.pos++
			return token{kind: tString, val: sb.String()}, nil
		}
		sb.WriteByte(c)
		l.pos++
	}
	return token{}, fmt.Errorf("%w: unterminated string literal", ErrParse)
}

func (l *lexer) lexQuotedIdent() (token, error) {
	l.pos++ // opening quote
	var sb strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '"' {
			l.pos++
			return token{kind: tIdent, val: sb.String()}, nil
		}
		sb.WriteByte(c)
		l.pos++
	}
	return token{}, fmt.Errorf("%w: unterminated quoted identifier", ErrParse)
}

func (l *lexer) lexNumber() (token, error) {
	start := l.pos
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' || c == 'e' || c == 'E' {
			l.pos++
			continue
		}
		break
	}
	return token{kind: tNumber, val: l.src[start:l.pos]}, nil
}

func (l *lexer) lexIdent() (token, error) {
	start := l.pos
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '_' || c == '[' || c == ']' || c == '*' || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) {
			l.pos++
			continue
		}
		break
	}
	return token{kind: tIdent, val: l.src[start:l.pos]}, nil
}

// ----- parser -----

type parser struct {
	tok []token
	pos int
}

// ParseSQL parses an S3 Select statement into a Query. It accepts the subset:
// projection (* / _N / name / alias.name), FROM S3Object[...] with optional
// alias, WHERE (comparisons joined by AND/OR/NOT with parentheses), and LIMIT.
func ParseSQL(expr string) (*Query, error) {
	toks, err := lex(expr)
	if err != nil {
		return nil, err
	}
	p := &parser{tok: toks}
	return p.parseSelect()
}

func (p *parser) cur() token { return p.tok[p.pos] }
func (p *parser) advance()   { p.pos++ }
func (p *parser) isKeyword(t token, kw string) bool {
	return t.kind == tIdent && strings.EqualFold(t.val, kw)
}

func (p *parser) expect(kind tokKind, what string) (token, error) {
	t := p.cur()
	if t.kind != kind {
		return token{}, fmt.Errorf("%w: expected %s", ErrParse, what)
	}
	p.advance()
	return t, nil
}

func (p *parser) parseSelect() (*Query, error) {
	if !p.isKeyword(p.cur(), "SELECT") {
		return nil, fmt.Errorf("%w: expected SELECT", ErrParse)
	}
	p.advance()

	q := &Query{}
	proj, err := p.parseProjection()
	if err != nil {
		return nil, err
	}
	q.Projection = proj

	if !p.isKeyword(p.cur(), "FROM") {
		return nil, fmt.Errorf("%w: expected FROM", ErrParse)
	}
	p.advance()
	alias, err := p.parseFrom()
	if err != nil {
		return nil, err
	}

	if p.isKeyword(p.cur(), "WHERE") {
		p.advance()
		where, err := p.parseExpr(alias)
		if err != nil {
			return nil, err
		}
		q.Where = where
	}

	if p.isKeyword(p.cur(), "LIMIT") {
		p.advance()
		t, err := p.expect(tNumber, "number after LIMIT")
		if err != nil {
			return nil, err
		}
		n, err := strconv.ParseInt(t.val, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%w: invalid LIMIT %q", ErrParse, t.val)
		}
		q.Limit = &n
	}

	if p.cur().kind != tEOF {
		return nil, fmt.Errorf("%w: unexpected trailing tokens", ErrParse)
	}
	return q, nil
}

func (p *parser) parseProjection() ([]SelectItem, error) {
	// SELECT *
	if p.cur().kind == tStar {
		p.advance()
		return nil, nil
	}
	var items []SelectItem
	for {
		ref, err := p.parseColumnRef("")
		if err != nil {
			return nil, err
		}
		items = append(items, SelectItem{Ref: ref})

		// Optional AS alias (parsed and ignored for now).
		if p.isKeyword(p.cur(), "AS") {
			p.advance()
			if _, err := p.expect(tIdent, "alias after AS"); err != nil {
				return nil, err
			}
		} else if p.cur().kind == tIdent && !p.isKeyword(p.cur(), "FROM") {
			// Bare alias (SELECT col x).
			p.advance()
		}

		if p.cur().kind == tComma {
			p.advance()
			continue
		}
		return items, nil
	}
}

// parseColumnRef parses _N, name, or alias.name. tableAlias, when non-empty, is
// stripped from "alias.name" references.
func (p *parser) parseColumnRef(tableAlias string) (ColumnRef, error) {
	t := p.cur()
	if t.kind != tIdent {
		return ColumnRef{}, fmt.Errorf("%w: expected column reference", ErrParse)
	}
	p.advance()

	// alias.name  (consume ".name")
	if p.cur().kind == tDot {
		p.advance()
		name := p.cur()
		if name.kind != tIdent && name.kind != tStar {
			return ColumnRef{}, fmt.Errorf("%w: expected column name after '.'", ErrParse)
		}
		p.advance()
		if name.kind == tStar {
			// alias.* is treated like a star elsewhere; not used in projection here.
			return ColumnRef{}, fmt.Errorf("%w: alias.* not supported", ErrParse)
		}
		return columnRefFromName(name.val), nil
	}

	return columnRefFromName(t.val), nil
}

// columnRefFromName turns an identifier into a ColumnRef, recognising the _N
// positional form.
func columnRefFromName(name string) ColumnRef {
	if len(name) > 1 && name[0] == '_' {
		if n, err := strconv.Atoi(name[1:]); err == nil && n >= 1 {
			return ColumnRef{Position: n}
		}
	}
	return ColumnRef{Name: name}
}

// parseFrom consumes "S3Object" (with optional [*] or path suffix) and an
// optional table alias, returning the alias (or "").
func (p *parser) parseFrom() (string, error) {
	t := p.cur()
	if t.kind != tIdent {
		return "", fmt.Errorf("%w: expected table name after FROM", ErrParse)
	}
	// Accept S3Object, S3Object[*], s3object, etc. The lexer keeps [ ] in the
	// identifier; we only require the S3Object prefix.
	base := t.val
	if idx := strings.IndexByte(base, '['); idx >= 0 {
		base = base[:idx]
	}
	if !strings.EqualFold(base, "S3Object") {
		return "", fmt.Errorf("%w: only FROM S3Object is supported", ErrParse)
	}
	p.advance()

	// Optional alias, unless the next token starts a clause.
	if p.cur().kind == tIdent && !p.isClauseKeyword(p.cur()) {
		alias := p.cur().val
		p.advance()
		return alias, nil
	}
	return "", nil
}

func (p *parser) isClauseKeyword(t token) bool {
	return p.isKeyword(t, "WHERE") || p.isKeyword(t, "LIMIT")
}

// parseExpr parses a boolean expression with precedence OR < AND < NOT < primary.
func (p *parser) parseExpr(alias string) (Expr, error) {
	return p.parseOr(alias)
}

func (p *parser) parseOr(alias string) (Expr, error) {
	left, err := p.parseAnd(alias)
	if err != nil {
		return nil, err
	}
	for p.isKeyword(p.cur(), "OR") {
		p.advance()
		right, err := p.parseAnd(alias)
		if err != nil {
			return nil, err
		}
		left = &boolExpr{op: opOr, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd(alias string) (Expr, error) {
	left, err := p.parseNot(alias)
	if err != nil {
		return nil, err
	}
	for p.isKeyword(p.cur(), "AND") {
		p.advance()
		right, err := p.parseNot(alias)
		if err != nil {
			return nil, err
		}
		left = &boolExpr{op: opAnd, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseNot(alias string) (Expr, error) {
	if p.isKeyword(p.cur(), "NOT") {
		p.advance()
		inner, err := p.parseNot(alias)
		if err != nil {
			return nil, err
		}
		return &notExpr{inner: inner}, nil
	}
	return p.parsePrimary(alias)
}

func (p *parser) parsePrimary(alias string) (Expr, error) {
	if p.cur().kind == tLParen {
		p.advance()
		inner, err := p.parseOr(alias)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	return p.parseComparison(alias)
}

func (p *parser) parseComparison(alias string) (Expr, error) {
	left, err := p.parseOperand(alias)
	if err != nil {
		return nil, err
	}
	opTok := p.cur()
	if opTok.kind != tOp {
		return nil, fmt.Errorf("%w: expected comparison operator", ErrParse)
	}
	p.advance()
	op, err := toCmpOp(opTok.val)
	if err != nil {
		return nil, err
	}
	right, err := p.parseOperand(alias)
	if err != nil {
		return nil, err
	}
	return &comparison{op: op, left: left, right: right}, nil
}

func (p *parser) parseOperand(alias string) (operand, error) {
	t := p.cur()
	switch t.kind {
	case tString:
		p.advance()
		return operand{literal: t.val, isLiteral: true}, nil
	case tNumber:
		p.advance()
		return operand{literal: t.val, isLiteral: true}, nil
	case tIdent:
		ref, err := p.parseColumnRef(alias)
		if err != nil {
			return operand{}, err
		}
		return operand{col: &ref}, nil
	default:
		return operand{}, fmt.Errorf("%w: expected column or literal", ErrParse)
	}
}

func toCmpOp(s string) (cmpOp, error) {
	switch s {
	case "=":
		return cmpEq, nil
	case "!=", "<>":
		return cmpNe, nil
	case "<":
		return cmpLt, nil
	case "<=":
		return cmpLe, nil
	case ">":
		return cmpGt, nil
	case ">=":
		return cmpGe, nil
	default:
		return 0, fmt.Errorf("%w: unknown operator %q", ErrParse, s)
	}
}
