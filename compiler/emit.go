package compiler

import (
	"fmt"
	"maps"
	"strings"
)

type emitter struct {
	lines   []string
	symbols []Symbol
}

func (e *emitter) line(format string, args ...any) {
	e.lines = append(e.lines, fmt.Sprintf(format, args...))
}

func (e *emitter) symbol(t token, generated, kind string) {
	e.symbols = append(e.symbols, Symbol{
		Generated: generated, Name: t.text, Kind: kind,
		Line: t.pos.line, Column: t.pos.column, GeneratedLine: len(e.lines) + 1,
	})
}

func emit(m *model, options Options) *Output {
	e := &emitter{}
	module := "Tlapr_" + m.name.text
	e.line("---- MODULE %s ----", module)
	e.line("EXTENDS Integers")
	e.line("")
	var stateNames []string
	initialNames := make(map[string]string)
	for _, v := range m.variables {
		name := "v_" + v.name.text
		e.symbol(v.name, name, "state")
		e.line("VARIABLE %s", name)
		stateNames = append(stateNames, name)
		initialNames[v.name.text] = name
	}
	e.line("")
	e.line("vars == <<%s>>", strings.Join(stateNames, ", "))
	e.line("")
	e.line("TypeOK ==")
	for _, v := range m.variables {
		domain := "Int"
		if v.typeTag.text == "Bool" {
			domain = "BOOLEAN"
		}
		e.line("    /\\ %s \\in %s", initialNames[v.name.text], domain)
	}
	e.line("")
	e.line("Init ==")
	for _, v := range m.variables {
		e.line("    /\\ %s = %s", initialNames[v.name.text], emitExpression(v.initial, initialNames))
	}
	for _, a := range m.actions {
		e.line("")
		e.symbol(a.name, "Action_"+a.name.text, "action")
		e.line("Action_%s ==", a.name.text)
		for _, guard := range a.guards {
			e.line("    /\\ %s", emitExpression(guard, initialNames))
		}
		// LET bindings preserve statement order without expanding repeated
		// assignments exponentially. Only the final values become next-state values.
		names := maps.Clone(initialNames)
		for i, u := range a.updates {
			rhs := emitExpression(u.value, names)
			if u.op.kind != "=" {
				rhs = "(" + names[u.name.text] + " " + u.op.text[:1] + " " + rhs + ")"
			}
			temporary := fmt.Sprintf("step%d", i+1)
			prefix := "           "
			if i == 0 {
				prefix = "    /\\ LET "
			}
			e.line("%s%s == %s", prefix, temporary, rhs)
			names[u.name.text] = temporary
		}
		var clauses []string
		var unchanged []string
		for _, v := range m.variables {
			name := v.name.text
			if names[name] == initialNames[name] {
				unchanged = append(unchanged, initialNames[name])
			} else {
				clauses = append(clauses, initialNames[name]+"' = "+names[name])
			}
		}
		if len(unchanged) > 0 {
			clauses = append(clauses, "UNCHANGED <<"+strings.Join(unchanged, ", ")+">>")
		}
		for i, clause := range clauses {
			prefix := "          /\\ "
			if i == 0 {
				prefix = "       IN /\\ "
			}
			e.line("%s%s", prefix, clause)
		}
	}
	e.line("")
	e.line("Next ==")
	for _, a := range m.actions {
		e.line("    \\/ Action_%s", a.name.text)
	}
	e.line("")
	e.line("Spec == Init /\\ [][Next]_vars")
	var config strings.Builder
	config.WriteString("SPECIFICATION Spec\nINVARIANT TypeOK\n")
	for i, inv := range m.invariants {
		name := "Invariant_" + inv.name.text
		if inv.anonymous {
			name = fmt.Sprintf("Invariant_%d", i+1)
		}
		e.line("")
		e.symbol(inv.name, name, "invariant")
		e.line("%s == %s", name, emitExpression(inv.condition, initialNames))
		config.WriteString("INVARIANT " + name + "\n")
	}
	e.line("")
	e.line("====")
	if options.AllowDeadlock {
		config.WriteString("CHECK_DEADLOCK FALSE\n")
	} else {
		config.WriteString("CHECK_DEADLOCK TRUE\n")
	}
	return &Output{Module: module, TLA: strings.Join(e.lines, "\n") + "\n", Config: config.String(), Symbols: e.symbols}
}

func emitExpression(e *expression, names map[string]string) string {
	if e.right == nil {
		switch {
		case e.token.kind == "integer":
			return integerText(e.token.text)
		case e.token.text == "true":
			return "TRUE"
		case e.token.text == "false":
			return "FALSE"
		default:
			return names[e.token.text]
		}
	}
	right := emitExpression(e.right, names)
	if e.left == nil {
		if e.token.kind == "!" {
			return "(~" + right + ")"
		}
		if right == "2147483648" {
			return "(-2147483647 - 1)"
		}
		return "(-" + right + ")"
	}
	op := e.token.text
	switch op {
	case "==":
		op = "="
	case "!=":
		op = "#"
	case "&&":
		op = "/\\"
	case "||":
		op = "\\/"
	}
	return "(" + emitExpression(e.left, names) + " " + op + " " + right + ")"
}

func integerText(text string) string {
	if trimmed := strings.TrimLeft(text, "0"); trimmed != "" {
		return trimmed
	}
	return "0"
}
