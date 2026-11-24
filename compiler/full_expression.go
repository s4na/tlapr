package compiler

import "strings"

// A module expression is parsed before it is rendered. Names are preserved in
// this mode: TLA+'s binder, module and proof scopes are checked by SANY.
type fullExpression struct {
	text        string
	infixBody   string
	opName      bool
	boundName   string
	boundDomain string
}

func (p *fullParser) fullExpr() string { return p.fullExprPrec(1, false).text }

func (p *fullParser) fullExprPrec(minimum int, stopCall bool) fullExpression {
	left := p.fullPrimary()
	for p.err == nil {
		t := p.peek()
		switch t.text {
		case "(":
			if stopCall || !left.opName {
				return left
			}
			args := p.fullArguments()
			if len(args) > 0 {
				left.text += fullCall(args)
			}
			continue
		case "[":
			p.take()
			args := p.fullExpressionList("]", false)
			left = fullExpression{text: left.text + "[" + strings.Join(args, ", ") + "]"}
			continue
		case ".":
			p.take()
			left = fullExpression{text: left.text + "." + p.fullName().text}
			continue
		case "!":
			if !left.opName {
				return left
			}
			p.take()
			left.text += "!" + p.fullSelector()
			continue
		case "'", "^+", "^*", "^#":
			if 15 < minimum {
				return left
			}
			p.take()
			left = fullExpression{text: "(" + left.text + t.text + ")"}
			continue
		}
		op, precedence := fullInfix(t)
		if precedence < minimum {
			return left
		}
		p.take()
		if op == "\\X" {
			// A Cartesian product is n-ary. (A X B) X C has a different
			// tuple shape from A X B X C, so do not nest a chain of products.
			parts := []string{left.text, p.fullExprPrec(precedence+1, false).text}
			for p.err == nil {
				next, _ := fullInfix(p.peek())
				if next != "\\X" {
					break
				}
				p.take()
				parts = append(parts, p.fullExprPrec(precedence+1, false).text)
			}
			body := strings.Join(parts, " \\X ")
			left = fullExpression{text: "(" + body + ")", infixBody: body}
			continue
		}
		right := p.fullExprPrec(precedence+1, false)
		body := left.text + " " + op + " " + right.text
		result := fullExpression{text: "(" + body + ")", infixBody: body}
		if op == "\\in" && fullBindingText(left.text) {
			result.boundName, result.boundDomain = left.text, right.text
		}
		left = result
	}
	return left
}

// Module mode has an explicit, conventional precedence table. Parentheses in
// the output make the chosen grouping independent of TLA+'s precedence ranges.
// A quoted operator keeps its native meaning (notably `&&`, `||`, and `//`).
func fullInfix(t token) (string, int) {
	if t.kind != "nativeop" && t.kind != "quotedname" {
		switch t.text {
		case "==":
			return "=", 5
		case "!=":
			return "#", 5
		case "&&":
			return "/\\", 4
		case "||":
			return "\\/", 3
		case "in":
			return "\\in", 5
		case "notin":
			return "\\notin", 5
		case "subsetof":
			return "\\subseteq", 5
		case "union":
			return "\\cup", 8
		case "intersect":
			return "\\cap", 8
		case "difference":
			return "\\", 8
		case "implies":
			return "=>", 1
		case "iff":
			return "<=>", 2
		case "leadsTo":
			return "~>", 2
		case "compose":
			return "\\cdot", 6
		case "cross":
			return "\\X", 10
		}
	}
	if t.kind == "quotedname" {
		return "", 0
	}
	switch t.text {
	case "=>":
		return t.text, 1
	case "<=>", "\\equiv", "~>", "-+->":
		return t.text, 2
	case "\\/", "\\lor":
		return "\\/", 3
	case "/\\", "\\land":
		return "/\\", 4
	case "=", "#", "/=", "<", "<=", "=<", ">", ">=", "\\in", "\\notin",
		"::=", ":=", "=|", "-|", "|-", "|=", "\\approx", "\\asymp", "\\cong",
		"\\doteq", "\\geq", "\\gg", "\\leq", "\\ll", "\\prec", "\\preceq",
		"\\propto", "\\sim", "\\simeq", "\\sqsubset", "\\sqsubseteq",
		"\\sqsupset", "\\sqsupseteq", "\\subset", "\\subseteq", "\\succ",
		"\\succeq", "\\supset", "\\supseteq":
		return t.text, 5
	case "@@", "\\cdot":
		return t.text, 6
	case ":>", "<:":
		return t.text, 7
	case "\\", "\\cap", "\\intersect", "\\cup", "\\union":
		return t.text, 8
	case "..", "...", "!!", "##", "$", "$$", "??", "\\sqcap", "\\sqcup", "\\uplus", "\\wr":
		return t.text, 9
	case "+", "-", "++", "--", "(+)", "(-)", "\\oplus", "\\ominus", "|", "\\bigcirc":
		return t.text, 10
	case "\\X", "\\times":
		return "\\X", 10
	case "*", "/", "//", "%", "%%", "**", "&", "&&", "||", "(.)", "(/)", "(\\X)",
		"\\odot", "\\oslash", "\\otimes", "\\bullet", "\\div", "\\o", "\\circ", "\\star":
		return t.text, 13
	case "^", "^^":
		return t.text, 14
	}
	return "", 0
}

func (p *fullParser) fullPrimary() fullExpression {
	t := p.take()
	if t.kind == "quotedname" {
		if !validFullName(t.text) || isTLAReserved(t.text) {
			p.fail(t, "invalid TLA+ identifier %q", t.text)
		}
		return p.fullNamedPrimary(t)
	}
	if t.kind == "number" || t.kind == "string" || t.kind == "stepref" {
		// The published grammar permits .5; SANY 2.1 requires its leading 0.
		if t.kind == "number" && strings.HasPrefix(t.text, ".") {
			t.text = "0" + t.text
		}
		return fullExpression{text: t.text, opName: t.kind == "stepref"}
	}
	if fullOperatorToken(t) && p.peek().text == "(" && p.fullCallHasSelector() {
		args := p.fullArguments()
		op := t.text
		if op == "-" && len(args) == 1 {
			op = "-."
		}
		return fullExpression{text: op + fullCall(args), opName: true}
	}
	if t.kind == "nativeop" {
		if fullArgumentEnd(p.peek().text) {
			return fullExpression{text: t.text, opName: true}
		}
		if !fullPrefixOperator(t.text) {
			p.fail(t, "operator %q needs a left operand or an operator-argument position", t.text)
			return fullExpression{text: t.text}
		}
	}
	switch t.text {
	case "true", "TRUE":
		return fullExpression{text: "TRUE"}
	case "false", "FALSE":
		return fullExpression{text: "FALSE"}
	case "@":
		return fullExpression{text: "@"}
	case "(":
		e := p.fullExprPrec(1, false)
		p.expect(")")
		return fullExpression{text: "(" + e.text + ")", infixBody: e.infixBody}
	case "{":
		return fullExpression{text: p.fullSet()}
	case "[":
		return fullExpression{text: p.fullBracket()}
	case "<<":
		return fullExpression{text: p.fullTuple()}
	case "!", "~", "\\lnot", "\\neg", "-", "-.", "always", "[]", "eventually", "<>",
		"enabled", "ENABLED", "unchanged", "UNCHANGED", "powerset", "subset", "SUBSET", "union", "UNION", "domain", "DOMAIN":
		op := t.text
		switch op {
		case "-.":
			op = "-"
		case "!", "\\lnot", "\\neg":
			op = "~"
		case "always":
			op = "[]"
		case "eventually":
			op = "<>"
		case "enabled", "unchanged", "union", "domain":
			op = strings.ToUpper(op)
		case "powerset", "subset":
			op = "SUBSET"
		}
		return fullExpression{text: "(" + op + " " + p.fullExprPrec(15, false).text + ")"}
	case "next":
		args := p.fullFixedArguments(t, 1)
		return fullExpression{text: "((" + args[0] + ")')"}
	case "forall", "\\A", "exists", "\\E", "temporalForall", "\\AA", "temporalExists", "\\EE":
		op := map[string]string{"forall": "\\A", "exists": "\\E", "temporalForall": "\\AA", "temporalExists": "\\EE"}[t.text]
		if op == "" {
			op = t.text
		}
		bounds := p.fullBounds(":", true)
		if (op == "\\AA" || op == "\\EE") && strings.Contains(bounds, "\\in") {
			p.fail(t, "temporal quantifiers take unbounded names")
		}
		p.expect(":")
		return fullExpression{text: "(" + op + " " + bounds + " : " + p.fullExpr() + ")"}
	case "choose", "CHOOSE":
		name := p.fullBindingName()
		if p.accept("in") || p.accept("\\in") {
			name += " \\in " + p.fullExpr()
		}
		p.expect(":")
		return fullExpression{text: "(CHOOSE " + name + " : " + p.fullExpr() + ")"}
	case "lambda", "LAMBDA":
		var names []string
		names = append(names, p.name().text)
		for p.accept(",") {
			names = append(names, p.name().text)
		}
		p.expect(":")
		// LAMBDA is an argument, not an ordinary parenthesized expression.
		return fullExpression{text: "LAMBDA " + strings.Join(names, ", ") + " : " + p.fullExpr()}
	case "if", "IF":
		condition := p.fullExpr()
		p.fullExpectEither("then", "THEN")
		yes := p.fullExpr()
		p.fullExpectEither("else", "ELSE")
		return fullExpression{text: "(IF " + condition + " THEN " + yes + " ELSE " + p.fullExpr() + ")"}
	case "case", "CASE":
		return fullExpression{text: p.fullCase()}
	case "let", "LET":
		return fullExpression{text: p.fullLet()}
	case "record", "recordset":
		p.expect("{")
		separator := "|->"
		if t.text == "recordset" {
			separator = ":"
		}
		return fullExpression{text: p.fullRecord("}", separator, ":")}
	case "function":
		p.expect("(")
		bounds := p.fullBounds(")", false)
		p.expect(")")
		p.expect("=>")
		return fullExpression{text: "[" + bounds + " |-> " + p.fullExpr() + "]"}
	case "functions":
		args := p.fullFixedArguments(t, 2)
		return fullExpression{text: "[" + args[0] + " -> " + args[1] + "]"}
	case "tuple":
		return fullExpression{text: "<<" + strings.Join(p.fullArguments(), ", ") + ">>"}
	case "update":
		args := p.fullFixedArguments(t, 1)
		p.expect("{")
		return fullExpression{text: p.fullExcept(args[0], "}", false)}
	case "all", "any":
		p.expect("{")
		var terms []string
		p.separators()
		for p.err == nil && p.peek().text != "}" && p.peek().kind != "eof" {
			terms = append(terms, p.fullExpr())
			if p.peek().text != "}" {
				p.expect(";")
			}
			p.separators()
		}
		p.expect("}")
		op := "/\\"
		if t.text == "any" {
			op = "\\/"
		}
		if len(terms) == 0 {
			p.fail(t, "%s needs at least one expression", t.text)
		}
		return fullExpression{text: fullJunction(op, terms)}
	case "/\\", "\\/":
		terms := []string{p.fullExprPrec(5, false).text}
		// Module syntax treats every newline as whitespace. Explicit
		// parentheses, not indentation, determine a nested junction.
		for p.err == nil && p.peek().text == t.text {
			p.take()
			terms = append(terms, p.fullExprPrec(5, false).text)
		}
		return fullExpression{text: fullJunction(t.text, terms)}
	case "weakFair", "strongFair", "stutter", "nonstutter":
		args := p.fullFixedArguments(t, 2)
		switch t.text {
		case "weakFair":
			return fullExpression{text: "WF_(" + args[0] + ")(" + args[1] + ")"}
		case "strongFair":
			return fullExpression{text: "SF_(" + args[0] + ")(" + args[1] + ")"}
		case "stutter":
			return fullExpression{text: "[" + args[0] + "]_(" + args[1] + ")"}
		default:
			return fullExpression{text: "<<" + args[0] + ">>_(" + args[1] + ")"}
		}
	case "WF_", "SF_":
		vars := p.fullExprPrec(16, true).text
		p.expect("(")
		action := p.fullExpr()
		p.expect(")")
		return fullExpression{text: t.text + "(" + vars + ")(" + action + ")"}
	case "ref":
		p.expect("(")
		name := p.name()
		p.expect(")")
		return fullExpression{text: p.fullProofReference(name), opName: true}
	default:
		if t.kind == "name" && validFullName(t.text) && !isTLAReserved(t.text) && !isFullReserved(t.text) && !fullExpressionBoundary(t.text) {
			return p.fullNamedPrimary(t)
		}
		p.fail(t, "expected an expression, got %q", t.text)
		return fullExpression{text: t.text}
	}
}

func fullExpressionBoundary(text string) bool {
	switch text {
	case "then", "THEN", "else", "ELSE", "in", "IN", "except", "EXCEPT", "OTHER",
		"def", "defs", "DEF", "DEFS", "prove", "PROVE", "proof", "PROOF":
		return true
	}
	return false
}

func fullPrefixOperator(text string) bool {
	switch text {
	case "-", "-.", "~", "\\lnot", "\\neg", "[]", "<>", "DOMAIN", "ENABLED", "SUBSET", "UNCHANGED", "UNION":
		return true
	}
	return false
}

func fullArgumentEnd(text string) bool {
	switch text {
	case ",", ")", ";", "}", "eof", "end of file":
		return true
	}
	return false
}

func (p *fullParser) fullNamedPrimary(t token) fullExpression {
	if p.accept("::") {
		return fullExpression{text: "(" + t.text + " :: " + p.fullExpr() + ")"}
	}
	// A label's parameters must be identifiers. Look ahead without consuming
	// ordinary operator applications, whose arguments may be arbitrary terms.
	if p.peek().text == "(" {
		i := 1
		var names []string
		for p.look(i).kind == "name" || p.look(i).kind == "quotedname" {
			names = append(names, p.look(i).text)
			i++
			if p.look(i).text != "," {
				break
			}
			i++
		}
		if len(names) > 0 && p.look(i).text == ")" && p.look(i+1).text == "::" {
			for range i + 2 {
				p.take()
			}
			return fullExpression{text: "(" + t.text + "(" + strings.Join(names, ", ") + ") :: " + p.fullExpr() + ")"}
		}
	}
	return fullExpression{text: t.text, opName: true}
}

func (p *fullParser) fullArgument() string {
	t := p.peek()
	if t.kind == "nativeop" && (fullArgumentEnd(p.look(1).text) || p.look(1).text == "!") {
		p.take()
		var name strings.Builder
		name.WriteString(t.text)
		for p.accept("!") {
			name.WriteString("!" + p.fullSelector())
		}
		return name.String()
	}
	// Native operator symbols are also accepted without quotes when there is
	// no operand. Quotes are needed for symbols reserved by the DSL lexer.
	if _, prec := fullInfix(t); prec > 0 && fullArgumentEnd(p.look(1).text) {
		p.take()
		return t.text
	}
	if (fullPrefixOperator(t.text) || t.text == "^+" || t.text == "^*" || t.text == "^#") && fullArgumentEnd(p.look(1).text) {
		p.take()
		return t.text
	}
	return p.fullExpr()
}

func (p *fullParser) fullArguments() []string {
	p.expect("(")
	var args []string
	if p.accept(")") {
		return args
	}
	for p.err == nil {
		args = append(args, p.fullArgument())
		if !p.accept(",") {
			break
		}
	}
	p.expect(")")
	return args
}

func (p *fullParser) fullFixedArguments(t token, count int) []string {
	args := p.fullArguments()
	if len(args) != count {
		p.fail(t, "%s expects %d arguments, got %d", t.text, count, len(args))
	}
	for len(args) < count {
		args = append(args, "")
	}
	return args
}

func (p *fullParser) fullSelector() string {
	t := p.take()
	switch {
	case t.kind == "number":
		if strings.Trim(t.text, "0123456789") != "" {
			p.fail(t, "a numeric subexpression selector must be a decimal integer")
		}
		return t.text
	case t.kind == "name" || t.kind == "quotedname" || t.kind == "nativeop" ||
		t.text == "<<" || t.text == ">>" || t.text == ":" || t.text == "@":
		return t.text
	case t.text == "(":
		p.index--
		return fullCall(p.fullArguments())
	default:
		if _, prec := fullInfix(t); prec > 0 || fullPrefixOperator(t.text) || t.text == "^+" || t.text == "^*" || t.text == "^#" {
			return t.text
		}
		p.fail(t, "expected a module or subexpression selector, got %q", t.text)
		return t.text
	}
}

func fullOperatorToken(t token) bool {
	if t.kind == "quotedname" {
		return false
	}
	_, infix := fullInfix(t)
	return t.kind == "nativeop" || infix > 0 || fullPrefixOperator(t.text) || t.text == "^+" || t.text == "^*" || t.text == "^#"
}

// A symbolic operator can head a positional reference: ++(a,b)!1. Ordinary
// prefix applications retain their normal meaning when no selector follows.
func (p *fullParser) fullCallHasSelector() bool {
	depth := 0
	for i := 0; p.look(i).kind != "eof"; i++ {
		t := p.look(i)
		if t.kind == "quotedname" || t.kind == "nativeop" || t.kind == "string" {
			continue
		}
		if t.text == "(" {
			depth++
		} else if t.text == ")" {
			depth--
			if depth == 0 {
				return p.look(i+1).text == "!"
			}
		}
	}
	return false
}

func fullCall(args []string) string {
	inside := strings.Join(args, ", ")
	// Spaces keep symbolic arguments distinct from (* / *) comments and the
	// single tokens (+), (-), (.), (/), and (\X).
	pad := strings.HasPrefix(inside, "*") || strings.HasSuffix(inside, "*")
	for _, argument := range args {
		_, precedence := fullInfix(token{kind: "nativeop", text: argument})
		if precedence > 0 || fullPrefixOperator(argument) || argument == "^+" || argument == "^*" || argument == "^#" {
			pad = true
		}
	}
	if pad {
		inside = " " + inside + " "
	}
	return "(" + inside + ")"
}

func (p *fullParser) fullExpressionList(end string, allowEmpty bool) []string {
	var terms []string
	if p.accept(end) {
		if !allowEmpty {
			p.fail(p.peek(), "expected at least one expression before %q", end)
		}
		return terms
	}
	for p.err == nil {
		terms = append(terms, p.fullExpr())
		if !p.accept(",") {
			break
		}
	}
	p.expect(end)
	return terms
}

func (p *fullParser) fullBindingName() string {
	if !p.accept("<<") {
		return p.name().text
	}
	var names []string
	names = append(names, p.name().text)
	for p.accept(",") {
		names = append(names, p.name().text)
	}
	p.expect(">>")
	return "<<" + strings.Join(names, ", ") + ">>"
}

// fullBounds leaves end unconsumed. It is shared by quantifiers, functions,
// function definitions, and TAKE/PICK proof steps.
func (p *fullParser) fullBounds(end string, allowUnbounded bool) string {
	var groups []string
	mode := ""
	for p.err == nil {
		start := p.peek()
		names := []string{p.fullBindingName()}
		for p.peek().text == "," {
			p.take()
			names = append(names, p.fullBindingName())
		}
		bounded := p.accept("in") || p.accept("\\in")
		current := "unbounded"
		text := strings.Join(names, ", ")
		if bounded {
			current = "bounded"
			text += " \\in " + p.fullExpr()
		} else if !allowUnbounded {
			p.fail(start, "expected a bounded name followed by in")
		}
		if !bounded {
			for _, name := range names {
				if strings.HasPrefix(name, "<<") {
					p.fail(start, "an unbounded quantifier takes names, not tuple patterns")
				}
			}
		}
		if mode != "" && mode != current {
			p.fail(start, "bounded and unbounded names cannot be mixed")
		}
		mode = current
		if len(names) > 1 {
			for _, name := range names {
				if strings.HasPrefix(name, "<<") {
					p.fail(start, "a tuple pattern needs its own in domain")
				}
			}
		}
		groups = append(groups, text)
		if p.peek().text == end || p.err != nil {
			break
		}
		if !p.accept(",") {
			p.fail(p.peek(), "expected a comma or %q after bound", end)
			break
		}
	}
	return strings.Join(groups, ", ")
}

func (p *fullParser) fullSet() string {
	if p.accept("}") {
		return "{}"
	}
	first := p.fullExprPrec(1, false)
	if p.accept(":") {
		if first.boundName != "" {
			predicate := p.fullExpr()
			p.expect("}")
			return "{" + first.boundName + " \\in " + first.boundDomain + " : " + predicate + "}"
		}
		bounds := p.fullBounds("}", false)
		p.expect("}")
		return "{" + first.text + " : " + bounds + "}"
	}
	terms := []string{first.text}
	for p.accept(",") {
		terms = append(terms, p.fullExpr())
	}
	p.expect("}")
	return "{" + strings.Join(terms, ", ") + "}"
}

func (p *fullParser) fullBracket() string {
	if (p.peek().kind == "name" || p.peek().kind == "quotedname") && (p.look(1).text == "|->" || p.look(1).text == ":") {
		separator := p.look(1).text
		return p.fullRecord("]", separator, separator)
	}
	if p.fullHasMapArrow() {
		bounds := p.fullBounds("|->", false)
		p.expect("|->")
		body := p.fullExpr()
		p.expect("]")
		return "[" + bounds + " |-> " + body + "]"
	}
	first := p.fullExpr()
	if p.accept("->") {
		codomain := p.fullExpr()
		p.expect("]")
		return "[" + first + " -> " + codomain + "]"
	}
	if p.accept("except") || p.accept("EXCEPT") {
		return p.fullExcept(first, "]", true)
	}
	if p.accept("]_") {
		return "[" + first + "]_(" + p.fullExprPrec(16, false).text + ")"
	}
	p.fail(p.peek(), "expected ->, EXCEPT, |->, or ]_ in bracket expression")
	return "[" + first + "]"
}

// Look ahead only for the bracket's own map arrow. Nested expressions are
// parsed exactly once, even for deeply nested sets and action expressions.
func (p *fullParser) fullHasMapArrow() bool {
	depth := 0
	for i := 0; p.look(i).kind != "eof"; i++ {
		t := p.look(i)
		if t.kind == "quotedname" || t.kind == "nativeop" || t.kind == "string" {
			continue
		}
		switch t.text {
		case "|->":
			if depth == 0 {
				return true
			}
		case "[", "{", "(", "<<":
			depth++
		case "]", "]_", "}", ")", ">>", ">>_":
			if depth == 0 {
				return false
			}
			depth--
		}
	}
	return false
}

func fullBindingText(text string) bool {
	if validFullName(text) {
		return true
	}
	if !strings.HasPrefix(text, "<<") || !strings.HasSuffix(text, ">>") {
		return false
	}
	for name := range strings.SplitSeq(text[2:len(text)-2], ",") {
		if !validFullName(strings.TrimSpace(name)) {
			return false
		}
	}
	return true
}

func (p *fullParser) fullRecord(end, outputSeparator, inputSeparator string) string {
	var fields []string
	if p.peek().text == end {
		p.fail(p.peek(), "a record expression needs at least one field")
	}
	for p.err == nil {
		name := p.fullName()
		p.expect(inputSeparator)
		fields = append(fields, name.text+" "+outputSeparator+" "+p.fullExpr())
		if p.peek().text == end {
			break
		}
		if !p.accept(",") && !p.accept(";") {
			break
		}
		if p.peek().text == end {
			break
		}
	}
	p.expect(end)
	return "[" + strings.Join(fields, ", ") + "]"
}

func (p *fullParser) fullExcept(original, end string, requireBang bool) string {
	var updates []string
	for p.err == nil {
		start := p.peek()
		if requireBang {
			p.expect("!")
		} else {
			p.accept("!")
		}
		var path strings.Builder
		for p.err == nil {
			if p.accept(".") {
				path.WriteString("." + p.fullName().text)
			} else if p.accept("[") {
				path.WriteString("[" + strings.Join(p.fullExpressionList("]", false), ", ") + "]")
			} else {
				break
			}
		}
		if path.Len() == 0 {
			p.fail(start, "an update needs a field or index path")
		}
		p.expect("=")
		updates = append(updates, "!"+path.String()+" = "+p.fullExpr())
		if p.peek().text == end {
			break
		}
		if !p.accept(",") && !p.accept(";") {
			break
		}
		if p.peek().text == end {
			break
		}
	}
	p.expect(end)
	return "[" + original + " EXCEPT " + strings.Join(updates, ", ") + "]"
}

func (p *fullParser) fullTuple() string {
	if p.accept(">>") {
		return "<<>>"
	}
	first := p.fullExpr()
	if p.accept(">>_") {
		return "<<" + first + ">>_(" + p.fullExprPrec(16, false).text + ")"
	}
	terms := []string{first}
	for p.accept(",") {
		terms = append(terms, p.fullExpr())
	}
	p.expect(">>")
	return "<<" + strings.Join(terms, ", ") + ">>"
}

func (p *fullParser) fullCase() string {
	braced := p.accept("{")
	var arms []string
	other := false
	for p.err == nil {
		condition := "OTHER"
		if p.accept("else") || p.accept("OTHER") {
			other = true
		} else {
			condition = p.fullExpr()
		}
		p.expect("->")
		arms = append(arms, condition+" -> "+p.fullExpr())
		if braced {
			if !p.accept(";") || p.peek().text == "}" {
				break
			}
		} else if !p.accept("[]") {
			break
		}
		if other {
			p.fail(p.peek(), "the else arm must be last")
		}
	}
	if braced {
		p.expect("}")
	}
	return "(CASE " + strings.Join(arms, " [] ") + ")"
}

func (p *fullParser) fullLet() string {
	p.expect("{")
	var definitions []string
	p.separators()
	for p.err == nil && p.peek().text != "}" && p.peek().kind != "eof" {
		if p.accept("recursive") {
			var names []string
			for p.err == nil {
				names = append(names, p.fullOpDecl())
				if !p.accept(",") {
					break
				}
			}
			definitions = append(definitions, "RECURSIVE "+strings.Join(names, ", "))
		} else {
			p.expect("def")
			definitions = append(definitions, p.fullDefinition())
		}
		p.expect(";")
		p.separators()
	}
	p.expect("}")
	p.fullExpectEither("in", "IN")
	if len(definitions) == 0 {
		p.fail(p.peek(), "let needs at least one definition")
	}
	return "(LET " + strings.Join(definitions, "\n    ") + "\n IN " + p.fullExpr() + ")"
}

func (p *fullParser) fullExpectEither(a, b string) {
	if !p.accept(a) && !p.accept(b) {
		p.fail(p.peek(), "expected %q, got %q", a, p.peek().text)
	}
}

func fullJunction(op string, terms []string) string {
	return "(\n  " + op + " " + strings.Join(terms, "\n  "+op+" ") + "\n)"
}
