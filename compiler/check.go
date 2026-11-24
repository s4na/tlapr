package compiler

import "strconv"

type valueType string

const (
	intType  valueType = "Int"
	boolType valueType = "Bool"
)

type checker struct {
	filename  string
	variables map[string]valueType
}

func check(filename string, m *model) error {
	c := checker{filename: filename, variables: make(map[string]valueType)}
	if len(m.variables) == 0 {
		return diagnostic(filename, m.name.pos, "a model needs at least one state variable")
	}
	if len(m.actions) == 0 {
		return diagnostic(filename, m.name.pos, "a model needs at least one action")
	}
	for _, v := range m.variables {
		if _, exists := c.variables[v.name.text]; exists {
			return diagnostic(filename, v.name.pos, "duplicate state variable %q", v.name.text)
		}
		c.variables[v.name.text] = valueType(v.typeTag.text)
	}
	for _, v := range m.variables {
		typ, err := c.expressionType(v.initial, true)
		if err != nil {
			return err
		}
		if typ != valueType(v.typeTag.text) {
			return diagnostic(filename, v.initial.token.pos, "initial value of %q must be %s, got %s", v.name.text, v.typeTag.text, typ)
		}
	}
	actions := make(map[string]bool)
	for _, a := range m.actions {
		if actions[a.name.text] {
			return diagnostic(filename, a.name.pos, "duplicate action %q", a.name.text)
		}
		actions[a.name.text] = true
		if len(a.updates) == 0 {
			return diagnostic(filename, a.name.pos, "an action needs at least one assignment (use x = x for an explicit no-op)")
		}
		for _, guard := range a.guards {
			if err := c.requireType(guard, boolType); err != nil {
				return err
			}
		}
		for _, u := range a.updates {
			targetType, exists := c.variables[u.name.text]
			if !exists {
				return diagnostic(filename, u.name.pos, "unknown state variable %q", u.name.text)
			}
			if u.op.kind != "=" && targetType != intType {
				return diagnostic(filename, u.op.pos, "%s requires an Int state variable", u.op.text)
			}
			if err := c.requireType(u.value, targetType); err != nil {
				return err
			}
		}
	}
	invariants := make(map[string]bool)
	for _, inv := range m.invariants {
		if invariants[inv.name.text] {
			return diagnostic(filename, inv.name.pos, "duplicate invariant %q", inv.name.text)
		}
		invariants[inv.name.text] = true
		if err := c.requireType(inv.condition, boolType); err != nil {
			return err
		}
	}
	return nil
}

func (c *checker) requireType(e *expression, expected valueType) error {
	actual, err := c.expressionType(e, false)
	if err != nil {
		return err
	}
	if actual != expected {
		return diagnostic(c.filename, e.token.pos, "expected %s expression, got %s", expected, actual)
	}
	return nil
}

func (c *checker) integer(t token, limit uint64) (valueType, error) {
	n, err := strconv.ParseUint(t.text, 10, 32)
	if err != nil || n > limit {
		return "", diagnostic(c.filename, t.pos, "integer literal is outside TLC's signed 32-bit range")
	}
	return intType, nil
}

func (c *checker) expressionType(e *expression, initial bool) (valueType, error) {
	t := e.token
	if e.right == nil {
		switch {
		case t.kind == "integer":
			return c.integer(t, 2147483647)
		case t.text == "true" || t.text == "false":
			return boolType, nil
		default:
			if initial {
				return "", diagnostic(c.filename, t.pos, "initial values must be constant expressions; state reference %q is not allowed", t.text)
			}
			if typ, exists := c.variables[t.text]; exists {
				return typ, nil
			}
			return "", diagnostic(c.filename, t.pos, "unknown state variable %q", t.text)
		}
	}
	// TLC cannot parse positive 2147483648, but the negative endpoint is valid.
	if e.left == nil && t.kind == "-" && e.right.token.kind == "integer" {
		return c.integer(e.right.token, 2147483648)
	}
	rightType, err := c.expressionType(e.right, initial)
	if err != nil {
		return "", err
	}
	if e.left == nil {
		expected := intType
		if t.kind == "!" {
			expected = boolType
		}
		if rightType != expected {
			return "", diagnostic(c.filename, t.pos, "operator %s requires %s, got %s", t.text, expected, rightType)
		}
		return expected, nil
	}
	leftType, err := c.expressionType(e.left, initial)
	if err != nil {
		return "", err
	}
	if leftType != rightType {
		return "", diagnostic(c.filename, t.pos, "operator %s has incompatible operands %s and %s", t.text, leftType, rightType)
	}
	switch t.kind {
	case "==", "!=":
		return boolType, nil
	case "&&", "||":
		if leftType != boolType {
			return "", diagnostic(c.filename, t.pos, "operator %s requires Bool operands", t.text)
		}
		return boolType, nil
	default:
		if leftType != intType {
			return "", diagnostic(c.filename, t.pos, "operator %s requires Int operands", t.text)
		}
		if t.kind == "+" || t.kind == "-" || t.kind == "*" {
			return intType, nil
		}
		return boolType, nil
	}
}
