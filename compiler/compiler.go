// Package compiler translates tlapr state models and structured modules into TLA+.
package compiler

import "fmt"

// Options controls TLC configuration; it does not change the transition relation.
type Options struct {
	AllowDeadlock bool
}

// Symbol connects a generated TLA+ identifier to its declaration in the DSL.
type Symbol struct {
	Generated     string `json:"generated"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Line          int    `json:"line"`
	Column        int    `json:"column"`
	GeneratedLine int    `json:"generatedLine"`
}

// Output contains the complete module, TLC configuration, and declaration map.
type Output struct {
	Module  string
	TLA     string
	Config  string
	Symbols []Symbol
}

// Diagnostic identifies a source error. Lines and columns are one-based.
type Diagnostic struct {
	Filename string
	Line     int
	Column   int
	Message  string
}

func (d *Diagnostic) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", d.Filename, d.Line, d.Column, d.Message)
}

// Compile parses source before emitting TLA+. Model mode also checks Int/Bool
// types; module mode uses SANY (tlapr validate) for TLA+ name and level checks.
// Compilation is deterministic and does not access the filesystem or execute input.
func Compile(filename string, source []byte, options Options) (*Output, error) {
	fullTokens, fullErr := lexFull(filename, source)
	if len(fullTokens) > 0 && fullTokens[0].kind == "name" && fullTokens[0].text == "module" {
		if fullErr != nil {
			return nil, fullErr
		}
		return compileFull(filename, fullTokens, options)
	}
	tokens, err := lex(filename, source)
	if err != nil {
		return nil, err
	}
	p := parser{filename: filename, tokens: tokens}
	m, err := p.parse()
	if err != nil {
		return nil, err
	}
	if err := check(filename, m); err != nil {
		return nil, err
	}
	return emit(m, options), nil
}

type position struct {
	line   int
	column int
}

func diagnostic(filename string, pos position, format string, args ...any) error {
	return &Diagnostic{filename, pos.line, pos.column, fmt.Sprintf(format, args...)}
}

type token struct {
	kind string
	text string
	pos  position
}

type expression struct {
	token token
	left  *expression
	right *expression
}

type variable struct {
	name    token
	typeTag token
	initial *expression
}

type assignment struct {
	name  token
	op    token
	value *expression
}

type action struct {
	name    token
	guards  []*expression
	updates []assignment
}

type invariant struct {
	name      token
	anonymous bool
	condition *expression
}

type model struct {
	name       token
	variables  []variable
	actions    []action
	invariants []invariant
}
