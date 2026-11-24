package compiler_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/s4na/tlapr/compiler"
)

func TestCompile(t *testing.T) {
	source := `model Inventory {
  state {
    stock: Int = 2
    sold: Int = 0
    paused: Bool = false
  }
  action purchase {
    require !paused && stock > 0
    stock -= 1
    sold += 1
  }
  invariant conserved { stock + sold == 2 }
}`
	out, err := compiler.Compile("inventory.tlapr", []byte(source), compiler.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := `---- MODULE Tlapr_Inventory ----
EXTENDS Integers

VARIABLE v_stock
VARIABLE v_sold
VARIABLE v_paused

vars == <<v_stock, v_sold, v_paused>>

TypeOK ==
    /\ v_stock \in Int
    /\ v_sold \in Int
    /\ v_paused \in BOOLEAN

Init ==
    /\ v_stock = 2
    /\ v_sold = 0
    /\ v_paused = FALSE

Action_purchase ==
    /\ ((~v_paused) /\ (v_stock > 0))
    /\ LET step1 == (v_stock - 1)
           step2 == (v_sold + 1)
       IN /\ v_stock' = step1
          /\ v_sold' = step2
          /\ UNCHANGED <<v_paused>>

Next ==
    \/ Action_purchase

Spec == Init /\ [][Next]_vars

Invariant_conserved == ((v_stock + v_sold) = 2)

====
`
	if out.TLA != want {
		t.Errorf("generated TLA+ differs:\n%s", out.TLA)
	}
	if wantConfig := "SPECIFICATION Spec\nINVARIANT TypeOK\nINVARIANT Invariant_conserved\nCHECK_DEADLOCK TRUE\n"; out.Config != wantConfig {
		t.Errorf("config = %q, want %q", out.Config, wantConfig)
	}
	wantSymbols := []compiler.Symbol{
		{Generated: "v_stock", Name: "stock", Kind: "state", Line: 3, Column: 5, GeneratedLine: 4},
		{Generated: "v_sold", Name: "sold", Kind: "state", Line: 4, Column: 5, GeneratedLine: 5},
		{Generated: "v_paused", Name: "paused", Kind: "state", Line: 5, Column: 5, GeneratedLine: 6},
		{Generated: "Action_purchase", Name: "purchase", Kind: "action", Line: 7, Column: 10, GeneratedLine: 20},
		{Generated: "Invariant_conserved", Name: "conserved", Kind: "invariant", Line: 12, Column: 13, GeneratedLine: 33},
	}
	if !reflect.DeepEqual(out.Symbols, wantSymbols) {
		t.Errorf("symbols = %#v", out.Symbols)
	}
	if out.Module != "Tlapr_Inventory" {
		t.Errorf("module = %q", out.Module)
	}
	for range 10 {
		again, err := compiler.Compile("inventory.tlapr", []byte(source), compiler.Options{})
		if err != nil || !reflect.DeepEqual(again, out) {
			t.Fatalf("compilation is not deterministic: %v", err)
		}
	}
	allowed, err := compiler.Compile("inventory.tlapr", []byte(source), compiler.Options{AllowDeadlock: true})
	if err != nil {
		t.Fatal(err)
	}
	if allowed.TLA != out.TLA || !strings.HasSuffix(allowed.Config, "CHECK_DEADLOCK FALSE\n") {
		t.Fatal("allowing deadlocks must change configuration only")
	}
}

func TestSyntaxAndExpressionPrecedence(t *testing.T) {
	source := `// 日本語コメント
model Integers
{
  state { Next: Int = 0002; flag: Bool = false }
  action TypeOK {
    require !(Next < 0) &&
      (Next != 3 || false)
    Next = Next + 2 * 3 - 1
    flag = Next == 7
  }
  invariant { !flag || Next == 7 }
}`
	out, err := compiler.Compile("syntax.tlapr", []byte(strings.ReplaceAll(source, "\n", "\r\n")), compiler.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"MODULE Tlapr_Integers", "v_Next = 2",
		"((~(v_Next < 0)) /\\ ((v_Next # 3) \\/ FALSE))",
		"step1 == ((v_Next + (2 * 3)) - 1)",
		"step2 == (step1 = 7)",
		"Invariant_1 == ((~v_flag) \\/ (v_Next = 7))",
	} {
		if !strings.Contains(out.TLA, fragment) {
			t.Errorf("missing %q in:\n%s", fragment, out.TLA)
		}
	}
}

func TestIntegerEndpoints(t *testing.T) {
	source := `model Edge { state { x: Int = -2147483648 } action stay { x = 2147483647 } }`
	out, err := compiler.Compile("edge.tlapr", []byte(source), compiler.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.TLA, "v_x = (-2147483647 - 1)") {
		t.Fatal("TLC's minimum integer must not require parsing positive 2147483648")
	}
}

func TestInvalidPrograms(t *testing.T) {
	wrap := func(state, action, inv string) string {
		return "model Test { state { " + state + " } action run { " + action + " } " + inv + " }"
	}
	tests := []struct {
		name    string
		source  string
		message string
	}{
		{"empty", "", "expected \"model\""},
		{"no state", "model X { action a { x = 0 } }", "at least one state"},
		{"no action", "model X { state { x: Int = 0 } }", "at least one action"},
		{"no assignment", wrap("x: Int = 0", "require true", ""), "at least one assignment"},
		{"duplicate state block", "model X { state {} state {} }", "one state block"},
		{"duplicate variable", wrap("x: Int = 0; x: Int = 1", "x = 1", ""), "duplicate state variable"},
		{"duplicate action", "model X { state { x: Int = 0 } action a { x = 1 } action a { x = 2 } }", "duplicate action"},
		{"duplicate invariant", wrap("x: Int = 0", "x = 0", "invariant ok { true } invariant ok { true }"), "duplicate invariant"},
		{"unknown read", wrap("x: Int = 0", "x = y", ""), "unknown state variable \"y\""},
		{"unknown write", wrap("x: Int = 0", "y = 1", ""), "unknown state variable \"y\""},
		{"initial reference", wrap("x: Int = 0; y: Int = x", "x = 0", ""), "initial values must be constant"},
		{"initial type", wrap("x: Int = true", "x = 0", ""), "initial value of \"x\" must be Int"},
		{"assignment type", wrap("x: Int = 0", "x = true", ""), "expected Int expression"},
		{"compound bool", wrap("x: Bool = true", "x += false", ""), "+= requires an Int"},
		{"guard type", wrap("x: Int = 0", "require x; x = 1", ""), "expected Bool expression"},
		{"invariant type", wrap("x: Int = 0", "x = 1", "invariant { x }"), "expected Bool expression"},
		{"boolean arithmetic", wrap("x: Bool = true", "x = true + false", ""), "requires Int operands"},
		{"integer logical", wrap("x: Bool = true", "x = 1 && 2", ""), "requires Bool operands"},
		{"mixed equality", wrap("x: Bool = true", "x = true == 0", ""), "incompatible operands"},
		{"bad unary", wrap("x: Bool = true", "x = !1", ""), "operator ! requires Bool"},
		{"guard after update", wrap("x: Int = 0", "x += 1; require x > 0", ""), "require must appear before"},
		{"missing separator", wrap("x: Int = 0", "x = 1 x = 2", ""), "expected a newline"},
		{"assignment as condition", wrap("x: Int = 0", "require x = 1; x = 0", ""), "expected a newline"},
		{"missing expression", wrap("x: Int = 0", "x =", ""), "expected an expression"},
		{"missing close", "model X { state { x: Int = 1 }", "expected \"}\""},
		{"missing parenthesis", wrap("x: Int = 0", "x = (1 + 2", ""), "expected \")\""},
		{"extra model", wrap("x: Int = 0", "x = 1", "") + " model Y {}", "one model"},
		{"unknown type", wrap("x: String = 0", "x = 1", ""), "expected type Int or Bool"},
		{"invalid character", wrap("x: Int = 0", "x = 1 / 2", ""), "unexpected character '/'"},
		{"reserved identifier", wrap("state: Int = 0", "state = 1", ""), "expected an identifier"},
		{"large positive", wrap("x: Int = 2147483648", "x = 0", ""), "signed 32-bit range"},
		{"large negative", wrap("x: Int = -2147483649", "x = 0", ""), "signed 32-bit range"},
		{"huge literal", wrap("x: Int = 9999999999999999999999", "x = 0", ""), "signed 32-bit range"},
		{"invalid utf8", string([]byte{0xff}), "valid UTF-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := compiler.Compile("invalid.tlapr", []byte(tt.source), compiler.Options{})
			if err == nil || out != nil {
				t.Fatalf("invalid input compiled: output=%v err=%v", out, err)
			}
			d, ok := errors.AsType[*compiler.Diagnostic](err)
			if !ok || d.Filename != "invalid.tlapr" || d.Line < 1 || d.Column < 1 {
				t.Fatalf("missing source diagnostic: %v", err)
			}
			if !strings.Contains(d.Message, tt.message) {
				t.Errorf("error = %q, want to contain %q", err, tt.message)
			}
		})
	}
}

func TestDiagnosticPosition(t *testing.T) {
	source := "model Test {\n  state { x: Int = 1 }\n  action run {\n    x = missing\n  }\n}"
	_, err := compiler.Compile("input.tlapr", []byte(source), compiler.Options{})
	if err == nil || err.Error() != "input.tlapr:4:9: unknown state variable \"missing\"" {
		t.Fatalf("diagnostic = %v", err)
	}
}

func FuzzCompile(f *testing.F) {
	for _, source := range []string{
		"model X { state { n: Int = 0 } action step { n += 1 } invariant { n >= 0 } }",
		"model B { state { b: Bool = false } action flip { b = !b } }", "", "model",
		"module M { def F(n) = let { recursive G(_); def G(x) = if x == 0 then n else G(x-1); } in G(1); }",
		"module M { var x; def Spec = x == 0 && always stutter(next(x) == x + 1, x); config { specification Spec; } }",
		"module M { theorem true; proof { step: assume { new x; } prove x == x; obvious; qed omitted; } }",
		"module M { def R = update(record {a: 1}) {.a = @ + 1;}; }",
	} {
		f.Add([]byte(source))
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		if len(source) > 16384 {
			t.Skip()
		}
		out, err := compiler.Compile("fuzz.tlapr", source, compiler.Options{})
		if err != nil {
			if out != nil {
				t.Fatal("invalid input returned output")
			}
			return
		}
		again, err := compiler.Compile("fuzz.tlapr", source, compiler.Options{})
		if err != nil || !reflect.DeepEqual(again, out) {
			t.Fatal("compilation is not deterministic")
		}
	})
}
