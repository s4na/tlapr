package compiler

import (
	"strings"
	"testing"
)

func TestFullExpressions(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{"literals", `tuple(0, 1.25, .5, \b101, \o7, \hff, "line\ntext", true, false)`, `<<0, 1.25, 0.5, \b101, \o7, \hff, "line\ntext", TRUE, FALSE>>`},
		{"precedence", `a + b * c == d || e && !f`, `(((a + (b * c)) = d) \/ (e /\ (~ f)))`},
		{"comparison", `x != y implies x notin S`, `((x # y) => (x \notin S))`},
		{"set_algebra", `S union T intersect U difference V`, `(((S \cup T) \cap U) \ V)`},
		{"native_operators", "a `&&` b + c `//` d", `((a && b) + (c // d))`},
		{"quoted_names", "`always`(`in`)", `always(in)`},
		{"bounded_quantifier", `forall x, y in S, z in T: x + y >= z`, `(\A x, y \in S, z \in T : ((x + y) >= z))`},
		{"tuple_quantifier", `exists <<x, y>> in S: x == y`, `(\E <<x, y>> \in S : (x = y))`},
		{"unbounded_quantifier", `forall x, y: x == y`, `(\A x, y : (x = y))`},
		{"temporal_quantifier", `temporalExists x: always x`, `(\EE x : ([] x))`},
		{"choose", `choose x in S: x > 0`, `(CHOOSE x \in S : (x > 0))`},
		{"choose_unbounded", `choose x: x == 0`, `(CHOOSE x : (x = 0))`},
		{"enumerated_set", `{1, 2, 3}`, `{1, 2, 3}`},
		{"empty_set", `{}`, `{}`},
		{"set_of_predicates", `{x in S}`, `{(x \in S)}`},
		{"filter_set", `{x in S: x > 1}`, `{x \in S : (x > 1)}`},
		{"image_set", `{x + y: x in S, y in T}`, `{(x + y) : x \in S, y \in T}`},
		{"tuple_filter", `{<<x, y>> in S: x == y}`, `{<<x, y>> \in S : (x = y)}`},
		{"function", `function(x in S, y in T) => x + y`, `[x \in S, y \in T |-> (x + y)]`},
		{"native_function", `[x \in S |-> x + 1]`, `[x \in S |-> (x + 1)]`},
		{"function_space", `functions(S, T)`, `[S -> T]`},
		{"native_function_space", `[S -> T]`, `[S -> T]`},
		{"record", `record {count: 1, ready: true}`, `[count |-> 1, ready |-> TRUE]`},
		{"record_set", `recordset {count: Int, ready: BOOLEAN}`, `[count : Int, ready : BOOLEAN]`},
		{"native_record", `[count |-> 1, ready |-> TRUE]`, `[count |-> 1, ready |-> TRUE]`},
		{"native_record_set", `[count: Int, ready: BOOLEAN]`, `[count : Int, ready : BOOLEAN]`},
		{"function_application", `f[x, y].count`, `f[x, y].count`},
		{"update", `update(f) { [x].count = @ + 1; [y] = 3; }`, `[f EXCEPT ![x].count = (@ + 1), ![y] = 3]`},
		{"native_update", `[f EXCEPT ![x] = @ + 1]`, `[f EXCEPT ![x] = (@ + 1)]`},
		{"native_tuple", `<<1, true>>`, `<<1, TRUE>>`},
		{"empty_tuple", `<<>>`, `<<>>`},
		{"nary_product", `A cross B cross C`, `(A \X B \X C)`},
		{"grouped_product", `(A cross B) cross C`, `(((A \X B)) \X C)`},
		{"prime", `next(x) == x + 1`, `(((x)') = (x + 1))`},
		{"prime_record_field", `next(r).field`, `((r)').field`},
		{"native_prime", `x' == x`, `((x') = x)`},
		{"stuttering", `stutter(A, tuple(x, y))`, `[A]_(<<x, y>>)`},
		{"native_stuttering", `[A]_x`, `[A]_(x)`},
		{"nonstuttering", `nonstutter(A, x)`, `<<A>>_(x)`},
		{"native_nonstuttering", `<<A>>_x`, `<<A>>_(x)`},
		{"weak_fairness", `weakFair(x, A)`, `WF_(x)(A)`},
		{"native_weak_fairness", `WF_x(A)`, `WF_(x)(A)`},
		{"native_strong_fairness", `SF_x(A)`, `SF_(x)(A)`},
		{"parenthesized_fairness", `SF_(x)(A)`, `SF_((x))(A)`},
		{"temporal", `always p leadsTo eventually q`, `(([] p) ~> (<> q))`},
		{"action_composition", `A compose B`, `(A \cdot B)`},
		{"if", `if x > 0 then x else -x`, `(IF (x > 0) THEN x ELSE (- x))`},
		{"case", `case {x == 0 -> 1; x == 1 -> 2; else -> 3;}`, `(CASE (x = 0) -> 1 [] (x = 1) -> 2 [] OTHER -> 3)`},
		{"native_case", `CASE x = 0 -> 1 [] OTHER -> 2`, `(CASE (x = 0) -> 1 [] OTHER -> 2)`},
		{"let", `let { def Double(x) = x * 2; } in Double(3)`, "(LET Double(x) == (x * 2)\n IN Double(3))"},
		{"let_recursive", `let { recursive F(_); def F(x) = if x == 0 then n else F(x - 1); } in F(3)`, "(LET RECURSIVE F(_)\n    F(x) == (IF (x = 0) THEN n ELSE F((x - 1)))\n IN F(3))"},
		{"higher_order", `Map(lambda x: x + 1, S)`, `Map(LAMBDA x : (x + 1), S)`},
		{"operator_argument", "Fold(`+`, S)", `Fold( +, S )`},
		{"unary_minus_argument", "Apply(`-.`, 1)", `Apply( -., 1 )`},
		{"multiplication_argument", "Apply(`*`)", `Apply( * )`},
		{"postfix_star_argument", "Apply(`^*`)", `Apply( ^* )`},
		{"single_addition_argument", "Apply(`+`)", `Apply( + )`},
		{"single_subtraction_argument", "Apply(`-`)", `Apply( - )`},
		{"single_division_argument", "Apply(`/`)", `Apply( / )`},
		{"reserved_field", "record {`IF`: 1}.`IF`", `[IF |-> 1].IF`},
		{"reserved_field_set", "recordset {`ELSE`: Int}", `[ELSE : Int]`},
		{"reserved_update_field", "update(r) { .`IF` = @ + 1; }", `[r EXCEPT !.IF = (@ + 1)]`},
		{"fairness_fields", `record {WF_: 1, SF_: 2}.WF_`, `[WF_ |-> 1, SF_ |-> 2].WF_`},
		{"quoted_fairness_fields", "record {`WF_`: 1, `SF_`: 2}.`SF_`", `[WF_ |-> 1, SF_ |-> 2].SF_`},
		{"native_fairness_fields", `[WF_ |-> 1, SF_ |-> 2]`, `[WF_ |-> 1, SF_ |-> 2]`},
		{"fairness_field_set", "recordset {WF_: Int, `SF_`: Int}", `[WF_ : Int, SF_ : Int]`},
		{"fairness_update_field", "update(r) { .WF_ = @ + 1; .`SF_` = 2; }", `[r EXCEPT !.WF_ = (@ + 1), !.SF_ = 2]`},
		{"infix_headed_selector", `++(1, 2)!1`, `++(1, 2)!1`},
		{"postfix_headed_selector", `^+(1)!2`, `^+(1)!2`},
		{"prefix_headed_selector", `-.(1)!:`, `-.(1)!:`},
		{"minus_headed_selector", `-(1)!:`, `-.(1)!:`},
		{"named_instance", `I(1)!F(2)`, `I(1)!F(2)`},
		{"instance_operator", "I!`+`(1, 2)", `I!+(1, 2)`},
		{"instance_prefix_operator", `I!DOMAIN`, `I!DOMAIN`},
		{"selector", `F!2!<<!:(x)!@`, `F!2!<<!:(x)!@`},
		{"argument_selector", `F!(x, y)!1`, `F!(x, y)!1`},
		{"label", `result :: x == y`, `(result :: (x = y))`},
		{"parameterized_label", `result(x) :: x == y`, `(result(x) :: (x = y))`},
		{"quoted_label_parameter", "result(`next`) :: `next`", `(result(next) :: next)`},
		{"all", `all { x > 0; x < 3; }`, "(\n  /\\ (x > 0)\n  /\\ (x < 3)\n)"},
		{"any", `any { x == 0; x == 2; }`, "(\n  \\/ (x = 0)\n  \\/ (x = 2)\n)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := fullExpressionParser(t, test.source)
			got := p.fullExpr()
			if p.err != nil {
				t.Fatal(p.err)
			}
			if p.peek().kind != "eof" {
				t.Fatalf("unparsed token: %#v", p.peek())
			}
			if got != test.want {
				t.Errorf("expression mismatch\n got: %s\nwant: %s", got, test.want)
			}
		})
	}
}

func TestFullExpressionErrors(t *testing.T) {
	for _, source := range []string{
		`forall x in S, y: x == y`,
		`forall <<x, y>>: x == y`,
		`forall <<x, y>>, z in S: x == y`,
		`temporalExists x in S: x == x`,
		`choose x in S x == 1`,
		`function(x) => x`,
		`record {}`,
		`update(f) { = 1; }`,
		`f[]`,
		`if x then y`,
		`case { else -> 0; x -> 1; }`,
		`let {} in x`,
		`all {}`,
		`next(x, y)`,
		`weakFair(x)`,
		`F!;`,
		`F!1.5`,
		`F!\h1`,
		`ref(missing)`,
		`{1, }`,
		`[x]`,
		`_`,
		"`IF`",
		"`WF_`",
		"`SF_`",
		`result(WF_) :: true`,
		"result(`SF_`) :: true",
		`I!SF_`,
		"I!`WF_`",
	} {
		t.Run(source, func(t *testing.T) {
			p := fullExpressionParser(t, source)
			p.fullExpr()
			if p.err == nil && p.peek().kind == "eof" {
				t.Fatal("invalid expression accepted")
			}
			if p.err != nil && !strings.HasPrefix(p.err.Error(), "expr.tlapr:1:") {
				t.Errorf("diagnostic has no source position: %v", p.err)
			}
		})
	}
}

func TestFullExpressionProofReference(t *testing.T) {
	p := fullExpressionParser(t, `ref(outer)!1`)
	p.proofScopes = []map[string]string{{"outer": "<1>step1"}, {"inner": "<2>step1"}}
	if got := p.fullExpr(); p.err != nil || got != "<1>step1!1" {
		t.Fatalf("got %q, error %v", got, p.err)
	}
}

func TestFullJunctionIgnoresWhitespace(t *testing.T) {
	for _, operator := range []string{`/\`, `\/`} {
		compact := operator + " true " + operator + " false " + operator + " true"
		p := fullExpressionParser(t, compact)
		want := p.fullExpr()
		if p.err != nil {
			t.Fatal(p.err)
		}
		for _, separator := range []string{"\n", "\n    ", "\n\t", "\r\n  "} {
			source := operator + " true" + separator + operator + " false" + separator + operator + " true"
			p := fullExpressionParser(t, source)
			if got := p.fullExpr(); p.err != nil || got != want || p.peek().kind != "eof" {
				t.Errorf("source=%q: output=%q, error=%v; want %q", source, got, p.err, want)
			}
		}
	}
}

func fullExpressionParser(t *testing.T, source string) *fullParser {
	t.Helper()
	tokens, err := lexFull("expr.tlapr", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return &fullParser{filename: "expr.tlapr", tokens: tokens}
}
