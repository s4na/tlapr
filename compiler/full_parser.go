package compiler

import (
	"slices"
	"strings"
)

// fullParser implements the module language. It does not impose model mode's
// Int/Bool type system on TLA+'s untyped set theory. SANY performs name/level
// validation; parsing here never executes expressions or accepts opaque blocks.
type fullParser struct {
	filename    string
	tokens      []token
	index       int
	err         error
	symbols     []Symbol
	proofScopes []map[string]string
	config      []string
	hasConfig   bool
}

type fullDeclaredName struct {
	source    token
	generated string
}

func fullFixity(t token) bool {
	return t.kind == "name" && (t.text == "prefix" || t.text == "infix" || t.text == "postfix")
}

func (p *fullParser) fullDeclarationName() fullDeclaredName {
	mode, name := p.peek(), p.peek()
	if fullFixity(mode) {
		name = p.look(1)
	}
	generated := name.text
	if fullFixity(mode) && mode.text == "prefix" && generated == "-" {
		generated = "-."
	}
	return fullDeclaredName{source: name, generated: generated}
}

func (p *fullParser) peek() token { return p.look(0) }
func (p *fullParser) look(n int) token {
	return p.tokens[min(p.index+n, len(p.tokens)-1)]
}
func (p *fullParser) take() token {
	t := p.peek()
	if t.kind != "eof" {
		p.index++
	}
	return t
}
func (p *fullParser) accept(text string) bool {
	if p.peek().text != text || p.peek().kind == "quotedname" || p.peek().kind == "nativeop" {
		return false
	}
	p.take()
	return true
}
func (p *fullParser) expect(text string) token {
	t := p.peek()
	if !p.accept(text) {
		p.fail(t, "expected %q, got %q", text, t.text)
	}
	return t
}
func (p *fullParser) fail(t token, format string, args ...any) {
	if p.err == nil {
		p.err = diagnostic(p.filename, t.pos, format, args...)
	}
}
func (p *fullParser) name() token {
	t := p.peek()
	if (t.kind != "name" && t.kind != "quotedname") || !validFullName(t.text) || isTLAReserved(t.text) || t.kind != "quotedname" && isFullReserved(t.text) {
		p.fail(t, "expected an identifier (quote DSL keywords with backticks), got %q", t.text)
		return t
	}
	return p.take()
}

// TLA+ uses Name, rather than Identifier, for module and record field names.
// Those positions permit reserved TLA+ words. DSL keywords still need quoting.
func (p *fullParser) fullName() token {
	t := p.peek()
	if (t.kind != "name" && t.kind != "quotedname") || !validFullName(t.text) || t.kind != "quotedname" && isFullReserved(t.text) {
		p.fail(t, "expected a name (quote DSL keywords with backticks), got %q", t.text)
		return t
	}
	return p.take()
}
func (p *fullParser) separators() {
	for p.accept(";") {
	}
}

func isTLAReserved(s string) bool {
	return slices.Contains(strings.Fields("ASSUME ELSE LOCAL UNION ASSUMPTION ENABLED MODULE VARIABLE AXIOM EXCEPT OTHER VARIABLES CASE EXTENDS SF_ WF_ CHOOSE IF SUBSET WITH CONSTANT IN THEN CONSTANTS INSTANCE THEOREM COROLLARY DOMAIN LET UNCHANGED BY HAVE QED TAKE DEF HIDE RECURSIVE USE DEFINE PROOF WITNESS PICK DEFS PROVE SUFFICES NEW LAMBDA STATE ACTION TEMPORAL OBVIOUS OMITTED LEMMA PROPOSITION ONLY"), s)
}
func isFullReserved(s string) bool {
	return slices.Contains(strings.Fields("module extends const var def recursive local instance with assume axiom theorem lemma proposition corollary proof by only obvious omitted step qed have witness take pick case suffices use hide define new constant variable state action temporal prove config prefix infix postfix forall exists temporalForall temporalExists choose if then else let in lambda record recordset function functions tuple update all any next always eventually enabled unchanged subset union domain weakFair strongFair stutter nonstutter ref true false powerset notin subsetof intersect difference implies iff leadsTo compose cross"), s)
}

func compileFull(filename string, tokens []token, options Options) (*Output, error) {
	p := &fullParser{filename: filename, tokens: tokens}
	module, source := p.fullModule(0)
	p.separators()
	if p.peek().kind != "eof" {
		p.fail(p.peek(), "only one root module is allowed per file")
	}
	if p.err != nil {
		return nil, p.err
	}
	var config string
	if p.hasConfig {
		if options.AllowDeadlock {
			p.config = slices.DeleteFunc(p.config, func(line string) bool { return strings.HasPrefix(line, "CHECK_DEADLOCK ") })
			p.config = append(p.config, "CHECK_DEADLOCK FALSE")
		} else if !slices.ContainsFunc(p.config, func(line string) bool { return strings.HasPrefix(line, "CHECK_DEADLOCK ") }) {
			p.config = append(p.config, "CHECK_DEADLOCK TRUE")
		}
		config = strings.Join(p.config, "\n") + "\n"
	}
	return &Output{Module: module, TLA: source + "\n", Config: config, Symbols: p.symbols}, nil
}

func (p *fullParser) fullModule(depth int) (string, string) {
	p.expect("module")
	name := p.fullName()
	lines := []string{"---- MODULE " + name.text + " ----"}
	if p.accept("extends") {
		var names []string
		for p.err == nil {
			names = append(names, p.fullName().text)
			if !p.accept(",") {
				break
			}
		}
		lines = append(lines, "EXTENDS "+strings.Join(names, ", "))
	}
	p.expect("{")
	p.separators()
	for p.err == nil && p.peek().text != "}" && p.peek().kind != "eof" {
		t := p.peek()
		local := p.accept("local")
		if local {
			t = p.peek()
		}
		var unit string
		var declarations []fullDeclaredName
		kind := "definition"
		switch t.text {
		case "const", "var", "recursive":
			p.take()
			if local {
				p.fail(t, "local applies only to definitions and instances")
			}
			keyword := map[string]string{"const": "CONSTANTS", "var": "VARIABLES", "recursive": "RECURSIVE"}[t.text]
			kind = map[string]string{"const": "constant", "var": "state", "recursive": "definition"}[t.text]
			var names []string
			for p.err == nil {
				declarations = append(declarations, p.fullDeclarationName())
				if t.text == "var" {
					names = append(names, p.name().text)
				} else {
					names = append(names, p.fullOpDecl())
				}
				if !p.accept(",") {
					break
				}
			}
			unit = keyword + " " + strings.Join(names, ", ")
			p.expect(";")
		case "def":
			p.take()
			declarations = []fullDeclaredName{p.fullDeclarationName()}
			unit = p.fullDefinition()
			p.expect(";")
		case "instance":
			unit = p.fullInstance()
			p.expect(";")
		case "assume", "axiom":
			p.take()
			if local {
				p.fail(t, "local cannot qualify an assumption")
			}
			unit = strings.ToUpper(t.text) + " "
			if (p.peek().kind == "name" || p.peek().kind == "quotedname") && p.look(1).text == "=" {
				decl := p.name()
				p.expect("=")
				declarations = []fullDeclaredName{{source: decl, generated: decl.text}}
				kind = "assumption"
				unit += decl.text + " == "
			}
			unit += p.fullExpr()
			p.expect(";")
		case "theorem", "lemma", "proposition", "corollary":
			p.take()
			if local {
				p.fail(t, "local cannot qualify a theorem")
			}
			unit = strings.ToUpper(t.text) + " "
			if (p.peek().kind == "name" || p.peek().kind == "quotedname") && p.look(1).text == "=" {
				decl := p.name()
				p.expect("=")
				declarations = []fullDeclaredName{{source: decl, generated: decl.text}}
				kind = "theorem"
				unit += decl.text + " == "
			}
			unit += p.fullProofStatement()
			p.expect(";")
			if slices.Contains([]string{"proof", "by", "obvious", "omitted"}, p.peek().text) {
				unit += "\n" + p.fullProof(1)
				p.accept(";")
			}
		case "use", "hide":
			p.take()
			if local {
				p.fail(t, "local cannot qualify use/hide")
			}
			unit = strings.ToUpper(t.text)
			if t.text == "hide" && p.peek().text == "only" {
				p.fail(p.peek(), "hide does not accept only")
			}
			if t.text == "use" && p.accept("only") {
				unit += " ONLY"
			}
			unit += " " + p.fullUseBody()
			p.expect(";")
		case "module":
			if local {
				p.fail(t, "local cannot qualify a nested module")
			}
			_, unit = p.fullModule(depth + 1)
			p.accept(";")
		case "config":
			if depth != 0 || local {
				p.fail(t, "config is allowed only in the root module")
			}
			p.fullConfig()
			p.accept(";")
		default:
			p.fail(t, "expected a module declaration, definition, assumption, theorem, proof directive, or config; got %q", t.text)
		}
		if unit != "" {
			if local {
				unit = "LOCAL " + unit
			}
			if depth == 0 {
				line := 1
				for _, previous := range lines {
					line += strings.Count(previous, "\n") + 1
				}
				for _, decl := range declarations {
					p.symbols = append(p.symbols, Symbol{Generated: decl.generated, Name: decl.source.text, Kind: kind, Line: decl.source.pos.line, Column: decl.source.pos.column, GeneratedLine: line})
				}
			}
			lines = append(lines, unit)
		}
		p.separators()
	}
	p.expect("}")
	lines = append(lines, "====")
	return name.text, strings.Join(lines, "\n")
}

// Called after 'def'; the caller owns the terminating semicolon.
func (p *fullParser) fullDefinition() string {
	var lhs string
	if mode := p.peek(); fullFixity(mode) {
		p.take()
		opToken := p.peek()
		op := p.fullOperator()
		p.fullCheckFixity(mode.text, opToken)
		p.expect("(")
		first := p.name().text
		switch mode.text {
		case "prefix":
			if op == "-" {
				op = "-."
			}
			lhs = op + " " + first
		case "postfix":
			lhs = first + " " + op
		case "infix":
			p.expect(",")
			lhs = first + " " + op + " " + p.name().text
		}
		p.expect(")")
	} else {
		lhs = p.name().text
		if p.accept("(") {
			var args []string
			for p.err == nil && p.peek().text != ")" {
				args = append(args, p.fullOpDecl())
				if !p.accept(",") {
					break
				}
			}
			p.expect(")")
			if len(args) > 0 {
				lhs += "(" + strings.Join(args, ", ") + ")"
			}
		} else if p.accept("[") {
			lhs += "[" + p.fullBounds("]", false) + "]"
			p.expect("]")
		}
	}
	p.expect("=")
	if p.peek().kind == "name" && p.peek().text == "instance" {
		return lhs + " == " + p.fullInstance()
	}
	return lhs + " == " + p.fullExpr()
}

func (p *fullParser) fullOperator() string {
	t := p.take()
	if t.kind == "nativeop" || slices.Contains(fullOperators, t.text) && !slices.Contains([]string{"{", "}", "(", ")", "[", "]", ";", ",", ":", "_", "@", "!", "<<", ">>", "]_", ">>_", "|->", "->", "<-", "==", "!="}, t.text) || slices.Contains([]string{"DOMAIN", "ENABLED", "SUBSET", "UNCHANGED", "UNION"}, t.text) {
		return t.text
	}
	p.fail(t, "expected a TLA+ operator symbol")
	return t.text
}

func (p *fullParser) fullCheckFixity(mode string, op token) {
	valid := false
	switch mode {
	case "prefix":
		valid = fullPrefixOperator(op.text)
	case "postfix":
		valid = slices.Contains([]string{"^+", "^*", "^#", "'"}, op.text)
	case "infix":
		_, priority := fullInfix(token{kind: "nativeop", text: op.text})
		valid = priority > 0 && fullProofSymbol(op.text)
	}
	if !valid {
		p.fail(op, "%q is not a TLA+ %s operator", op.text, mode)
	}
}

func (p *fullParser) fullOpDecl() string {
	if t := p.peek(); fullFixity(t) {
		p.take()
		opToken := p.peek()
		op := p.fullOperator()
		p.fullCheckFixity(t.text, opToken)
		switch t.text {
		case "prefix":
			if op == "-" {
				op = "-."
			}
			return op + " _"
		case "infix":
			return "_ " + op + " _"
		default:
			return "_ " + op
		}
	}
	name := p.name().text
	if p.accept("(") {
		var args []string
		for p.err == nil && p.peek().text != ")" {
			p.expect("_")
			args = append(args, "_")
			if !p.accept(",") {
				break
			}
		}
		p.expect(")")
		if len(args) == 0 {
			p.fail(p.peek(), "operator declarations need at least one placeholder")
		}
		name += "(" + strings.Join(args, ", ") + ")"
	}
	return name
}

func (p *fullParser) fullInstance() string {
	p.expect("instance")
	result := "INSTANCE " + p.fullName().text
	if p.accept("with") {
		var substitutions []string
		for p.err == nil {
			var name string
			if p.peek().kind == "nativeop" {
				name = p.fullOperator()
			} else {
				name = p.name().text
			}
			if !p.accept("<-") {
				p.expect("=")
			}
			substitutions = append(substitutions, name+" <- "+p.fullArgument())
			if !p.accept(",") {
				break
			}
		}
		result += " WITH " + strings.Join(substitutions, ", ")
	}
	return result
}

func (p *fullParser) fullConfig() {
	t := p.expect("config")
	if p.hasConfig {
		p.fail(t, "only one config block is allowed")
	}
	p.hasConfig = true
	p.expect("{")
	seen := make(map[string]bool)
	for p.err == nil && p.peek().text != "}" && p.peek().kind != "eof" {
		key := p.take()
		keyword := map[string]string{"specification": "SPECIFICATION", "init": "INIT", "next": "NEXT", "invariant": "INVARIANT", "property": "PROPERTY", "constraint": "CONSTRAINT", "actionConstraint": "ACTION_CONSTRAINT", "symmetry": "SYMMETRY", "view": "VIEW", "constant": "CONSTANT", "checkDeadlock": "CHECK_DEADLOCK"}[key.text]
		if keyword == "" {
			p.fail(key, "unknown TLC config directive %q", key.text)
			break
		}
		switch key.text {
		case "constant":
			name := p.name().text
			op := p.take()
			if op.text != "=" && op.text != "<-" {
				p.fail(op, "expected = or <- in a constant setting")
			}
			var module string
			if p.accept("[") {
				module = "[" + p.fullName().text + "] "
				p.expect("]")
			}
			var value string
			if op.text == "<-" {
				value = p.name().text
			} else {
				value = p.fullConfigValue()
			}
			p.config = append(p.config, keyword+" "+name+" "+op.text+" "+module+value)
		case "checkDeadlock":
			value := p.take()
			if value.text != "true" && value.text != "false" {
				p.fail(value, "checkDeadlock requires true or false")
			}
			if seen[key.text] {
				p.fail(key, "duplicate config directive %q", key.text)
			}
			p.config = append(p.config, keyword+" "+strings.ToUpper(value.text))
		default:
			multiple := slices.Contains([]string{"invariant", "property", "constraint", "actionConstraint"}, key.text)
			if !multiple && seen[key.text] {
				p.fail(key, "duplicate config directive %q", key.text)
			}
			for p.err == nil {
				p.config = append(p.config, keyword+" "+p.name().text)
				if !multiple || !p.accept(",") {
					break
				}
			}
		}
		seen[key.text] = true
		p.expect(";")
	}
	p.expect("}")
	if seen["specification"] && (seen["init"] || seen["next"]) {
		p.fail(t, "config must choose specification or init/next")
	}
	if seen["init"] != seen["next"] {
		p.fail(t, "config requires both init and next")
	}
}

func (p *fullParser) fullConfigValue() string {
	t := p.take()
	switch {
	case t.text == "{":
		var values []string
		for p.err == nil && p.peek().text != "}" {
			values = append(values, p.fullConfigValue())
			if !p.accept(",") {
				break
			}
		}
		p.expect("}")
		return "{" + strings.Join(values, ", ") + "}"
	case t.text == "-":
		p.fail(t, "negative TLC config values require a definition override (constant N <- Value)")
		return ""
	case t.text == "true", t.text == "false":
		return strings.ToUpper(t.text)
	case t.kind == "string":
		return t.text
	case t.kind == "number":
		if strings.Trim(t.text, "0123456789") != "" {
			p.fail(t, "config values require decimal integers")
		}
		return t.text
	case (t.kind == "name" || t.kind == "quotedname") && validFullName(t.text) && !isTLAReserved(t.text):
		return t.text
	default:
		p.fail(t, "expected a TLC constant value, got %q", t.text)
		return ""
	}
}
