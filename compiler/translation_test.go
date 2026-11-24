package compiler_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/s4na/tlapr/compiler"
)

func TestTranslationOperators(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		resultType string
		want       string
	}{
		{"addition", "x + y", "Int", "(v_x + v_y)"},
		{"subtraction", "x - y", "Int", "(v_x - v_y)"},
		{"multiplication", "x * y", "Int", "(v_x * v_y)"},
		{"negation", "-x", "Int", "(-v_x)"},
		{"integer equality", "x == y", "Bool", "(v_x = v_y)"},
		{"integer inequality", "x != y", "Bool", "(v_x # v_y)"},
		{"less", "x < y", "Bool", "(v_x < v_y)"},
		{"less or equal", "x <= y", "Bool", "(v_x <= v_y)"},
		{"greater", "x > y", "Bool", "(v_x > v_y)"},
		{"greater or equal", "x >= y", "Bool", "(v_x >= v_y)"},
		{"boolean equality", "a == b", "Bool", "(v_a = v_b)"},
		{"boolean inequality", "a != b", "Bool", "(v_a # v_b)"},
		{"conjunction", "a && b", "Bool", "(v_a /\\ v_b)"},
		{"disjunction", "a || b", "Bool", "(v_a \\/ v_b)"},
		{"not", "!a", "Bool", "(~v_a)"},
		{"left associative subtraction", "x - y - 1", "Int", "((v_x - v_y) - 1)"},
		{"multiplication before addition", "x + y * 2", "Int", "(v_x + (v_y * 2))"},
		{"parentheses override precedence", "(x + y) * 2", "Int", "((v_x + v_y) * 2)"},
		{"unary before multiplication", "-x * y", "Int", "((-v_x) * v_y)"},
		{"boolean precedence", "a || b && !a", "Bool", "(v_a \\/ (v_b /\\ (~v_a)))"},
		{"comparison before equality", "x + 1 < y == a && b", "Bool", "((((v_x + 1) < v_y) = v_a) /\\ v_b)"},
		{"left associative equality", "a == b == a", "Bool", "((v_a = v_b) = v_a)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initial := "0"
			if test.resultType == "Bool" {
				initial = "false"
			}
			source := fmt.Sprintf(`model Operators {
  state { x: Int = 2; y: Int = 3; a: Bool = true; b: Bool = false; result: %s = %s }
  action evaluate { result = %s }
}`, test.resultType, initial, test.expression)
			out, err := compiler.Compile("operators.tlapr", []byte(source), compiler.Options{})
			if err != nil {
				t.Fatal(err)
			}
			want := "    /\\ LET step1 == " + test.want + "\n"
			if !strings.Contains(out.TLA, want) {
				t.Errorf("%q must translate to %q; got:\n%s", test.expression, test.want, out.TLA)
			}
		})
	}
}

func TestTranslationAssignmentScope(t *testing.T) {
	source := `model Actions {
  state { x: Int = 1; y: Int = 2; untouched: Bool = false }
  action first {
    require x > 0
    x += y
    y = x
    x *= y
    x -= 1
  }
  action second {
    require y < 3
    y = x
    x = y
  }
  invariant start_state { x >= 0 && y >= 0 }
}`
	out, err := compiler.Compile("actions.tlapr", []byte(source), compiler.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`Action_first ==
    /\ (v_x > 0)
    /\ LET step1 == (v_x + v_y)
           step2 == step1
           step3 == (step1 * step2)
           step4 == (step3 - 1)
       IN /\ v_x' = step4
          /\ v_y' = step2
          /\ UNCHANGED <<v_untouched>>
`,
		`Action_second ==
    /\ (v_y < 3)
    /\ LET step1 == v_x
           step2 == step1
       IN /\ v_x' = step2
          /\ v_y' = step1
          /\ UNCHANGED <<v_untouched>>
`,
		"Invariant_start_state == ((v_x >= 0) /\\ (v_y >= 0))\n",
	} {
		if !strings.Contains(out.TLA, want) {
			t.Errorf("assignment values escaped their action or were evaluated out of order; missing:\n%s\ngot:\n%s", want, out.TLA)
		}
	}
}

func TestTranslationDistinctNamesAndLocations(t *testing.T) {
	source := `model Integers {
  state {
    Init: Int = 0
    step1: Int = 1
  }
  action Init { Init = step1; step1 = Init }
  invariant Init { Init >= 0 }
  invariant { step1 >= 0 }
  invariant Invariant_2 { true }
}`
	out, err := compiler.Compile("names.tlapr", []byte(source), compiler.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		generated string
		name      string
		kind      string
		line      int
		column    int
	}{
		{"v_Init", "Init", "state", 3, 5},
		{"v_step1", "step1", "state", 4, 5},
		{"Action_Init", "Init", "action", 6, 10},
		{"Invariant_Init", "Init", "invariant", 7, 13},
		{"Invariant_2", "invariant#2", "invariant", 8, 3},
		{"Invariant_Invariant_2", "Invariant_2", "invariant", 9, 13},
	}
	if len(out.Symbols) != len(want) {
		t.Fatalf("symbols = %#v, want %d distinct declarations", out.Symbols, len(want))
	}
	lines := strings.Split(out.TLA, "\n")
	for i, expected := range want {
		got := out.Symbols[i]
		if got.Generated != expected.generated || got.Name != expected.name || got.Kind != expected.kind ||
			got.Line != expected.line || got.Column != expected.column {
			t.Errorf("symbol %d = %#v, want %#v", i, got, expected)
		}
		if got.GeneratedLine < 1 || got.GeneratedLine > len(lines) {
			t.Fatalf("invalid generated line for %s: %d", got.Generated, got.GeneratedLine)
		}
		prefix := expected.generated + " =="
		if expected.kind == "state" {
			prefix = "VARIABLE " + expected.generated
		}
		if !strings.HasPrefix(lines[got.GeneratedLine-1], prefix) {
			t.Errorf("source map for %s points to %q, want its declaration", got.Generated, lines[got.GeneratedLine-1])
		}
		if expected.kind == "invariant" && strings.Count(out.Config, "INVARIANT "+expected.generated+"\n") != 1 {
			t.Errorf("TLC must check %s exactly once; config:\n%s", expected.generated, out.Config)
		}
	}
}
