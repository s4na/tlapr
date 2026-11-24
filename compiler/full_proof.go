package compiler

import (
	"fmt"
	"slices"
	"strings"
)

// fullProof translates the brace structure into TLA+'s numbered proof levels.
// The trailing semicolon belongs to the surrounding declaration or proof step.
// This checks the proof's structure; SANY checks names and levels, while checking
// the actual proof obligations is the responsibility of a proof checker.
func (p *fullParser) fullProof(level int) string {
	p.fullProofAccept("proof")
	if p.peek().kind == "quotedname" || p.peek().kind == "nativeop" {
		p.fail(p.peek(), "expected a proof block, by, obvious, or omitted")
		return ""
	}
	switch t := p.peek(); strings.ToLower(t.text) {
	case "by":
		p.take()
		return "PROOF BY " + p.fullUseBody()
	case "obvious", "omitted":
		p.take()
		return "PROOF " + strings.ToUpper(t.text)
	case "{":
		p.take()
	default:
		p.fail(t, "expected a proof block, by, obvious, or omitted")
		return ""
	}

	p.proofScopes = append(p.proofScopes, make(map[string]string))
	defer func() { p.proofScopes = p.proofScopes[:len(p.proofScopes)-1] }()
	var lines []string
	for p.err == nil && p.peek().text != "}" && p.peek().kind != "eof" {
		p.separators()
		if p.fullProofAccept("qed") {
			lines = append(lines, fmt.Sprintf("  <%d> QED", level))
			p.separators()
			if p.fullProofStart() {
				lines = append(lines, fullProofIndent(p.fullProof(level+1), "    "))
				p.separators()
			}
			if p.peek().text != "}" {
				p.fail(p.peek(), "qed must be the final step of its proof block")
			}
			p.expect("}")
			return "PROOF\n" + strings.Join(lines, "\n")
		}
		if !p.fullProofAccept("step") {
			p.fail(p.peek(), "expected step or qed in a proof block")
			break
		}
		label := fmt.Sprintf("<%d>", level)
		if p.peek().text != ":" {
			name := p.name()
			scope := p.proofScopes[len(p.proofScopes)-1]
			if _, exists := scope[name.text]; exists {
				p.fail(name, "duplicate proof step %q", name.text)
			}
			label = fmt.Sprintf("<%d>step%d", level, len(scope)+1)
			scope[name.text] = label
		}
		p.expect(":")
		statement, canProve := p.fullProofStepStatement()
		lines = append(lines, "  "+label+". "+statement)
		p.expect(";")
		p.separators()
		if p.fullProofStart() {
			if !canProve {
				p.fail(p.peek(), "this proof step does not accept a nested proof")
				break
			}
			lines = append(lines, fullProofIndent(p.fullProof(level+1), "    "))
			p.separators()
		}
	}
	if p.err == nil {
		p.fail(p.peek(), "a proof block must end with qed")
	}
	return ""
}

func (p *fullParser) fullProofStart() bool {
	if p.peek().kind == "quotedname" || p.peek().kind == "nativeop" {
		return false
	}
	switch strings.ToLower(p.peek().text) {
	case "proof", "by", "obvious", "omitted":
		return true
	}
	return false
}

func (p *fullParser) fullProofAccept(word string) bool {
	if p.fullProofIs(word) {
		p.take()
		return true
	}
	return false
}

func (p *fullParser) fullProofIs(word string) bool {
	return p.peek().kind != "quotedname" && p.peek().kind != "nativeop" && strings.EqualFold(p.peek().text, word)
}

func fullProofIndent(text, prefix string) string {
	return prefix + strings.ReplaceAll(text, "\n", "\n"+prefix)
}

// fullProofReference is used only by the explicit ref(name) expression. Ordinary
// identifiers are never rewritten, even when they share a proof step's name.
func (p *fullParser) fullProofReference(name token) string {
	for _, scope := range slices.Backward(p.proofScopes) {
		if generated, ok := scope[name.text]; ok {
			return generated
		}
	}
	p.fail(name, "unknown proof step %q", name.text)
	return ""
}

func (p *fullParser) fullProofStepStatement() (string, bool) {
	t := p.peek()
	if t.kind == "quotedname" || t.kind == "nativeop" {
		return p.fullProofStatement(), true
	}
	switch strings.ToLower(t.text) {
	case "@":
		// A proof @ step is a separate TLA+ production, not an EXCEPT
		// expression. Its outer @ relation must not be parenthesized.
		p.take()
		op := p.take()
		symbol, precedence := fullInfix(op)
		if precedence == 0 {
			p.fail(op, "expected an infix operator after a proof-step @")
		}
		return "@ " + symbol + " " + p.fullExpr(), true
	case "use", "hide":
		p.take()
		if strings.EqualFold(t.text, "hide") && p.fullProofIs("only") {
			p.fail(p.peek(), "hide does not accept only")
		}
		return strings.ToUpper(t.text) + " " + p.fullUseBody(), false
	case "have":
		p.take()
		return "HAVE " + p.fullExpr(), false
	case "witness":
		p.take()
		return "WITNESS " + p.fullProofExpressionList(), false
	case "take":
		p.take()
		return "TAKE " + p.fullBounds(";", true), false
	case "pick":
		p.take()
		bound := p.fullBounds(":", true)
		p.expect(":")
		return "PICK " + bound + " : " + p.fullExpr(), true
	case "case":
		p.take()
		return "CASE " + p.fullExpr(), true
	case "suffices":
		p.take()
		return "SUFFICES " + p.fullProofStatement(), true
	case "define":
		p.take()
		p.expect("{")
		var definitions []string
		p.separators()
		for p.err == nil && p.peek().text != "}" && p.peek().kind != "eof" {
			p.expect("def")
			definitions = append(definitions, p.fullDefinition())
			p.expect(";")
			p.separators()
		}
		if len(definitions) == 0 {
			p.fail(p.peek(), "define needs at least one definition")
		}
		p.expect("}")
		return "DEFINE " + strings.Join(definitions, "\n             "), false
	case "def":
		p.take()
		return "DEFINE " + p.fullDefinition(), false
	case "instance":
		return p.fullInstance(), false
	default:
		return p.fullProofStatement(), true
	}
}

func (p *fullParser) fullProofExpressionList() string {
	values := []string{p.fullExpr()}
	for p.err == nil && p.accept(",") {
		values = append(values, p.fullExpr())
	}
	return strings.Join(values, ", ")
}

// The block form makes the assumption list's boundary explicit, especially when
// an assumption is itself an ASSUME/PROVE statement with its own local NEW names.
func (p *fullParser) fullProofStatement() string {
	if !p.fullProofAccept("assume") {
		expr := p.fullExprPrec(1, false)
		// SANY recognizes a following @ step from the preceding statement's
		// syntactic top-level infix operator. Keep that operator unwrapped,
		// while retaining the generated grouping of both operands.
		if expr.infixBody != "" {
			return expr.infixBody
		}
		return expr.text
	}
	block := p.accept("{")
	var assumptions []string
	for p.err == nil {
		if block && p.peek().text == "}" {
			break
		}
		assumptions = append(assumptions, p.fullProofAssumption())
		if block {
			if p.peek().text == "}" {
				break
			}
			if !p.accept(";") && !p.accept(",") {
				p.fail(p.peek(), "expected a semicolon, comma, or closing brace after an assumption")
				break
			}
			p.separators()
		} else if !p.accept(",") {
			break
		}
	}
	if len(assumptions) == 0 {
		p.fail(p.peek(), "assume needs at least one assumption")
	}
	if block {
		p.expect("}")
	}
	if !p.fullProofAccept("prove") {
		p.fail(p.peek(), "expected prove after the assumptions")
	}
	return "ASSUME " + strings.Join(assumptions, ", ") + " PROVE " + p.fullExpr()
}

func (p *fullParser) fullProofAssumption() string {
	word := strings.ToLower(p.peek().text)
	if p.peek().kind == "quotedname" || p.peek().kind == "nativeop" {
		word = ""
	}
	switch word {
	case "new", "const", "constant", "var", "variable", "state", "action", "temporal":
		return p.fullNewDeclaration()
	case "assume":
		return p.fullProofStatement()
	}
	if (p.peek().kind == "name" || p.peek().kind == "quotedname") && p.look(1).text == "::" && strings.EqualFold(p.look(2).text, "assume") && p.look(2).kind != "quotedname" {
		label := p.name().text
		p.expect("::")
		return label + ":: " + p.fullProofStatement()
	}
	return p.fullExpr()
}

func (p *fullParser) fullNewDeclaration() string {
	var parts []string
	if p.fullProofAccept("new") {
		parts = append(parts, "NEW")
	}
	if level, ok := map[string]string{
		"const": "CONSTANT", "constant": "CONSTANT", "var": "VARIABLE", "variable": "VARIABLE",
		"state": "STATE", "action": "ACTION", "temporal": "TEMPORAL",
	}[strings.ToLower(p.peek().text)]; ok && p.peek().kind != "quotedname" {
		p.take()
		parts = append(parts, level)
	}
	if len(parts) == 0 {
		p.fail(p.peek(), "expected new or a declaration level")
		return ""
	}
	if (p.peek().kind == "name" || p.peek().kind == "quotedname") && (p.look(1).text == "in" || p.look(1).text == "\\in") && p.look(1).kind != "quotedname" {
		name := p.name().text
		p.take()
		parts = append(parts, name+" \\in "+p.fullExpr())
	} else {
		parts = append(parts, p.fullOpDecl())
	}
	return strings.Join(parts, " ")
}

// fullUseBody is shared by module USE/HIDE, proof steps and terminal BY proofs.
// It parses facts separately from the DEF names instead of passing proof text
// through to TLA+ unchecked.
func (p *fullParser) fullUseBody() string {
	only := p.fullProofAccept("only")
	var facts, definitions []string
	if !p.fullProofIs("def") && !p.fullProofIs("defs") {
		for p.err == nil {
			if p.fullProofAccept("module") {
				facts = append(facts, "MODULE "+p.fullName().text)
			} else {
				facts = append(facts, p.fullExpr())
			}
			if !p.accept(",") {
				break
			}
		}
	}
	if p.fullProofAccept("def") || p.fullProofAccept("defs") {
		for p.err == nil {
			if p.fullProofAccept("module") {
				definitions = append(definitions, "MODULE "+p.fullName().text)
			} else {
				definitions = append(definitions, p.fullOpName())
			}
			if !p.accept(",") {
				break
			}
		}
	}
	if len(facts) == 0 && len(definitions) == 0 {
		p.fail(p.peek(), "by, use, and hide need at least one fact or definition")
	}
	result := strings.Join(facts, ", ")
	if len(definitions) > 0 {
		if result != "" {
			result += " "
		}
		result += "DEF " + strings.Join(definitions, ", ")
	}
	if only {
		result = "ONLY " + result
	}
	return result
}

// DEF accepts operator names, including instance and subexpression selectors,
// rather than arbitrary expressions. Symbolic names may be quoted in the DSL
// when their spelling is also a DSL operator or comment delimiter.
func (p *fullParser) fullOpName() string {
	var result string
	if p.peek().text == "ref" && p.peek().kind != "quotedname" && p.look(1).text == "(" {
		p.take()
		p.expect("(")
		result = p.fullProofReference(p.name())
		p.expect(")")
	} else {
		result = p.fullProofNamePart(false)
	}
	for p.err == nil && p.accept("!") {
		result += "!" + p.fullProofNamePart(true)
	}
	return result
}

func (p *fullParser) fullProofNamePart(selector bool) string {
	t := p.peek()
	if fullProofSymbol(t.text) || t.kind == "stepref" {
		p.take()
		return t.text
	}
	if selector && (t.text == "<<" || t.text == ">>" || t.text == "@" || t.kind == "number" && strings.Trim(t.text, "0123456789") == "") {
		p.take()
		return t.text
	}
	if t.kind == "name" || t.kind == "quotedname" {
		return p.name().text
	}
	p.fail(t, "expected an operator name")
	return ""
}

func fullProofSymbol(text string) bool {
	// These are exactly the prefix/infix/postfix names from TLAPlus2Grammar,
	// excluding delimiters and the DSL-only aliases in fullOperators.
	for symbol := range strings.FieldsSeq(`- -. ~ \lnot \neg [] <> DOMAIN ENABLED SUBSET UNCHANGED UNION
!! # ## $ $$ % %% & && (+) (-) (.) (/) (\X) * ** + ++ - -+-> -- -| .. ... / // /= /\
::= := :> < <: <=> = =< => =| > >= ?? @@ \ \/ ^ ^^ | |- |= || ~> . <=
\approx \geq \oslash \sqsupseteq \asymp \gg \otimes \star \bigcirc \in \prec \subset
\bullet \intersect \preceq \subseteq \cap \land \propto \succ \cdot \leq \sim \succeq
\circ \ll \simeq \supset \cong \lor \sqcap \supseteq \cup \o \sqcup \union \div \odot
\sqsubset \uplus \doteq \ominus \sqsubseteq \wr \equiv \oplus \sqsupset \notin ^+ ^* ^# '`) {
		if text == symbol {
			return true
		}
	}
	return false
}
