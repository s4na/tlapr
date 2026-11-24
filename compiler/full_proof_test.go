package compiler

import (
	"os"
	"strings"
	"testing"
)

func parseFullProofForTest(t *testing.T, source string) (string, error) {
	t.Helper()
	tokens, err := lexFull("proof.tlapr", []byte(source))
	if err != nil {
		return "", err
	}
	p := fullParser{filename: "proof.tlapr", tokens: tokens}
	result := p.fullProof(1)
	p.separators()
	if p.err == nil && p.peek().kind != "eof" {
		t.Fatalf("proof left an unexpected token: %v", p.peek())
	}
	return result, p.err
}

func TestFullProofForms(t *testing.T) {
	tests := []struct {
		name, source string
		want         []string
	}{
		{"obvious", "proof obvious;", []string{"PROOF OBVIOUS"}},
		{"omitted", "omitted;", []string{"PROOF OMITTED"}},
		{"by", "by only true def F, M!G;", []string{"PROOF BY ONLY TRUE DEF F, M!G"}},
		{"module_references", "by module Naturals def module Naturals;", []string{"PROOF BY MODULE Naturals DEF MODULE Naturals"}},
		{"quoted_keyword_fact", "by `def`;", []string{"PROOF BY def"}},
		{"reserved_module_name", "by module `IF` def module `IF`;", []string{"PROOF BY MODULE IF DEF MODULE IF"}},
		{
			"unnamed_steps_preserve_usable_facts",
			`proof { step: true; obvious; step named: true; obvious; step: use true; qed by ref(named); }`,
			[]string{"<1>. TRUE", "<1>step1. TRUE", "<1>. USE TRUE", "PROOF BY <1>step1"},
		},
		{
			"relation_chain",
			`proof { step a: 1 <= 2; obvious; step b: @ <= 3; obvious; step: @ <= 4 + 1; obvious; qed by ref(a), ref(b); }`,
			[]string{"<1>step1. 1 <= 2", "<1>step2. @ <= 3", "<1>. @ <= (4 + 1)"},
		},
		{
			"named_steps",
			`proof { step user_name: true; obvious; qed by ref(user_name); }`,
			[]string{"<1>step1. TRUE", "PROOF OBVIOUS", "<1> QED", "PROOF BY <1>step1"},
		},
		{
			"nested_scopes",
			`proof {
  step shared: true;
  proof {
    step shared: true; obvious;
    qed by ref(shared);
  }
  qed by ref(shared);
}`,
			[]string{"<1>step1. TRUE", "<2>step1. TRUE", "PROOF BY <2>step1", "PROOF BY <1>step1"},
		},
		{
			"proof_steps",
			`proof {
  step a: use only true def F;
  step b: hide false def G;
  step c: have true;
  step d: witness 1, 2;
  step e: take x, y;
  step f: pick z in {1, 2}: z > 0; omitted;
  step g: case true; obvious;
  step h: suffices true; omitted;
  step i: define { def One = 1; def Identity(x) = x; };
  step j: instance Naturals;
  qed omitted;
}`,
			[]string{
				"USE ONLY TRUE DEF F", "HIDE FALSE DEF G", "HAVE TRUE", "WITNESS 1, 2", "TAKE x, y",
				"PICK z \\in {1, 2} :", "CASE TRUE", "SUFFICES TRUE", "DEFINE One == 1", "Identity(x) == x", "INSTANCE Naturals",
			},
		},
		{
			"new_levels_and_nested_assumptions",
			`proof {
  step a: assume {
    new constant P(_); variable v; state S; action A; temporal T;
    new x in {1, 2};
    nested:: assume { new y; y == y; } prove y == y;
  } prove x == x;
  omitted;
  qed omitted;
}`,
			[]string{"ASSUME NEW CONSTANT P(_), VARIABLE v, STATE S, ACTION A, TEMPORAL T, NEW x \\in {1, 2}", "nested:: ASSUME NEW y", "PROVE"},
		},
		{
			"inline_assumptions_and_direct_definition",
			`proof { step a: assume new x, true prove true; omitted; step b: def F = 1; qed omitted; }`,
			[]string{"ASSUME NEW x, TRUE PROVE TRUE", "DEFINE F == 1"},
		},
		{
			"symbolic_definitions_and_subexpressions",
			"by def `+`, `^+`, M!`++`, M!1, M!@;",
			[]string{"DEF +, ^+, M!++, M!1, M!@"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := parseFullProofForTest(t, test.source)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(result, want) {
					t.Errorf("proof is missing %q:\n%s", want, result)
				}
			}
		})
	}
}

func TestFullProofDiagnostics(t *testing.T) {
	tests := []struct{ source, want string }{
		{"proof {}", "must end with qed"},
		{"proof { step a: true; }", "must end with qed"},
		{"proof { qed; step a: true; }", "final step"},
		{"proof { step a: true; step a: true; qed; }", "duplicate proof step"},
		{"proof { step a: true; proof { step nested: true; qed; } qed by ref(nested); }", "unknown proof step"},
		{"proof { qed by ref(missing); }", "unknown proof step"},
		{"proof { step a: have true; obvious; qed; }", "does not accept a nested proof"},
		{"proof { step a: hide only true; qed; }", "hide does not accept only"},
		{"proof { step a: assume {} prove true; qed; }", "at least one assumption"},
		{"proof { step a: define {}; qed; }", "at least one definition"},
		{"proof { step a: true qed; }", "expected"},
		{"by def 42;", "operator name"},
		{"proof `obvious`;", "expected a proof block"},
		{"proof { step a: @ true; qed; }", "infix operator after a proof-step @"},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			_, err := parseFullProofForTest(t, test.source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if !strings.HasPrefix(err.Error(), "proof.tlapr:") {
				t.Fatalf("diagnostic lost the source position: %v", err)
			}
		})
	}
}

func TestFullProofReferenceDoesNotRewriteIdentifiers(t *testing.T) {
	result, err := parseFullProofForTest(t, `proof { step same: same == same; qed by ref(same); }`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "same = same") || !strings.Contains(result, "PROOF BY <1>step1") {
		t.Fatalf("ordinary names and explicit proof references must remain distinct:\n%s", result)
	}
}

func TestFullProofModuleUseOnlyKeepsMeaning(t *testing.T) {
	const filename = "testdata/tool_limits/module_use_only.tlapr"
	source, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Compile(filename, source, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.TLA, "USE ONLY TRUE\n") {
		t.Fatalf("SANY's limitation must not silently change USE ONLY into USE:\n%s", out.TLA)
	}
}
