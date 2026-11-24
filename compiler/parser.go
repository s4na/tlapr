package compiler

import "fmt"

type parser struct {
	filename string
	tokens   []token
	index    int
	err      error
}

func (p *parser) peek() token { return p.tokens[p.index] }

func (p *parser) take() token {
	t := p.peek()
	if t.kind != "eof" {
		p.index++
	}
	return t
}

func (p *parser) fail(t token, format string, args ...any) {
	if p.err == nil {
		p.err = diagnostic(p.filename, t.pos, format, args...)
	}
}

func (p *parser) expect(text string) token {
	t := p.peek()
	if t.text != text {
		p.fail(t, "expected %q, got %q", text, t.text)
		return t
	}
	return p.take()
}

func (p *parser) name() token {
	t := p.peek()
	if t.kind != "name" || isKeyword(t.text) {
		p.fail(t, "expected an identifier, got %q", t.text)
		return t
	}
	return p.take()
}

func (p *parser) newlines() {
	for p.peek().kind == "newline" {
		p.take()
	}
}

func (p *parser) separators() {
	for p.peek().kind == "newline" || p.peek().kind == ";" {
		p.take()
	}
}

func (p *parser) endStatement() {
	if t := p.peek(); t.kind != "newline" && t.kind != ";" && t.kind != "}" {
		p.fail(t, "expected a newline, semicolon, or closing brace after the expression")
	}
	p.separators()
}

func (p *parser) openBlock() {
	p.newlines()
	p.expect("{")
	p.separators()
}

func (p *parser) inBlock() bool {
	return p.err == nil && p.peek().kind != "}" && p.peek().kind != "eof"
}

func (p *parser) parse() (*model, error) {
	p.separators()
	p.expect("model")
	m := &model{name: p.name()}
	p.openBlock()
	hasState := false
	for p.inBlock() {
		switch t := p.take(); t.text {
		case "state":
			if hasState {
				p.fail(t, "only one state block is allowed")
			}
			hasState = true
			p.openBlock()
			for p.inBlock() {
				v := variable{name: p.name()}
				p.expect(":")
				v.typeTag = p.take()
				if v.typeTag.text != "Int" && v.typeTag.text != "Bool" {
					p.fail(v.typeTag, "expected type Int or Bool, got %q", v.typeTag.text)
				}
				p.expect("=")
				p.newlines()
				v.initial = p.expression(1)
				m.variables = append(m.variables, v)
				p.endStatement()
			}
			p.expect("}")
		case "action":
			a := action{name: p.name()}
			p.openBlock()
			for p.inBlock() {
				if p.peek().text == "require" {
					t := p.take()
					if len(a.updates) > 0 {
						p.fail(t, "require must appear before assignments in an action")
					}
					p.newlines()
					a.guards = append(a.guards, p.expression(1))
				} else {
					u := assignment{name: p.name(), op: p.take()}
					switch u.op.kind {
					case "=", "+=", "-=", "*=":
					default:
						p.fail(u.op, "expected assignment =, +=, -=, or *=")
					}
					p.newlines()
					u.value = p.expression(1)
					a.updates = append(a.updates, u)
				}
				p.endStatement()
			}
			p.expect("}")
			m.actions = append(m.actions, a)
		case "invariant":
			inv := invariant{name: t, anonymous: true}
			if p.peek().kind == "name" {
				inv.name = p.name()
				inv.anonymous = false
			} else {
				inv.name.text = fmt.Sprintf("invariant#%d", len(m.invariants)+1)
			}
			p.openBlock()
			inv.condition = p.expression(1)
			p.separators()
			p.expect("}")
			m.invariants = append(m.invariants, inv)
		default:
			p.fail(t, "expected state, action, or invariant, got %q", t.text)
		}
		p.separators()
	}
	p.expect("}")
	p.separators()
	if p.peek().kind != "eof" {
		p.fail(p.peek(), "only one model is allowed per file")
	}
	if p.err != nil {
		return nil, p.err
	}
	return m, nil
}

func precedence(kind string) int {
	switch kind {
	case "||":
		return 1
	case "&&":
		return 2
	case "==", "!=":
		return 3
	case "<", "<=", ">", ">=":
		return 4
	case "+", "-":
		return 5
	case "*":
		return 6
	}
	return 0
}

func (p *parser) expression(minPrecedence int) *expression {
	left := p.primary()
	for p.err == nil {
		prec := precedence(p.peek().kind)
		if prec < minPrecedence {
			break
		}
		op := p.take()
		p.newlines()
		left = &expression{token: op, left: left, right: p.expression(prec + 1)}
	}
	return left
}

func (p *parser) primary() *expression {
	t := p.take()
	switch {
	case t.kind == "integer" || t.text == "true" || t.text == "false":
		return &expression{token: t}
	case t.kind == "name" && !isKeyword(t.text):
		return &expression{token: t}
	case t.kind == "!" || t.kind == "-":
		p.newlines()
		return &expression{token: t, right: p.primary()}
	case t.kind == "(":
		p.newlines()
		e := p.expression(1)
		p.newlines()
		p.expect(")")
		return e
	default:
		p.fail(t, "expected an expression, got %q", t.text)
		return &expression{token: t}
	}
}
