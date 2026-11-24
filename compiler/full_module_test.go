package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s4na/tlapr/compiler"
)

func fullModuleFixturePaths(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("testdata/modules/*.tlapr")
	if err != nil || len(paths) == 0 {
		t.Fatalf("module fixtures: %v", err)
	}
	return append(paths, "../examples/queue.tlapr", "../examples/proof.tlapr")
}

func TestFullModuleFixtures(t *testing.T) {
	paths := fullModuleFixturePaths(t)
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			out, err := compiler.Compile(path, source, compiler.Options{})
			if err != nil {
				t.Fatal(err)
			}
			again, err := compiler.Compile(path, source, compiler.Options{})
			if err != nil || out.TLA != again.TLA || out.Config != again.Config {
				t.Fatalf("nondeterministic compilation: %v", err)
			}
			if !strings.HasPrefix(out.TLA, "---- MODULE "+out.Module+" ----\n") || !strings.HasSuffix(out.TLA, "\n====\n") {
				t.Fatalf("invalid module envelope:\n%s", out.TLA)
			}
		})
	}
}

func TestFullModuleConfiguration(t *testing.T) {
	source := []byte(`module Configuration extends Integers {
  const Limit, Values;
  var x;
  def Init = x == 0;
  def Next = next(x) == (if x == Limit then 0 else x + 1);
  def Spec = Init && always stutter(Next, x);
  def Safe = x in Values;
  def Live = eventually (x == Limit);
  config {
    specification Spec;
    constant Limit = 2;
    constant Values = {0, 1, 2};
    invariant Safe;
    property Live;
    checkDeadlock false;
  }
}`)
	out, err := compiler.Compile("config.tlapr", source, compiler.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := "SPECIFICATION Spec\nCONSTANT Limit = 2\nCONSTANT Values = {0, 1, 2}\nINVARIANT Safe\nPROPERTY Live\nCHECK_DEADLOCK FALSE\n"
	if out.Config != want {
		t.Fatalf("config:\n%s\nwant:\n%s", out.Config, want)
	}
	if out.Module != "Configuration" {
		t.Fatalf("module name = %q", out.Module)
	}
	for _, symbol := range out.Symbols {
		lines := strings.Split(out.TLA, "\n")
		if symbol.GeneratedLine < 1 || symbol.GeneratedLine > len(lines) || !strings.Contains(lines[symbol.GeneratedLine-1], symbol.Generated) {
			t.Errorf("invalid declaration source mapping: %+v", symbol)
		}
	}
}

func TestFullModuleConfigBooleanValues(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"quoted true", "`true`", "true"},
		{"quoted false", "`false`", "false"},
		{"boolean true", "true", "TRUE"},
		{"boolean false", "false", "FALSE"},
		{"string true", `"true"`, `"true"`},
		{"string false", `"false"`, `"false"`},
		{"mixed set", "{`true`, true, `false`, false}", "{true, TRUE, false, FALSE}"},
		{"nested set", "{{`true`, false}, {`false`, true}}", "{{true, FALSE}, {false, TRUE}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte("module ConfigValues { const Value; config { constant Value = " + tc.value + "; } }")
			out, err := compiler.Compile("config_values.tlapr", source, compiler.Options{})
			if err != nil {
				t.Fatal(err)
			}
			want := "CONSTANT Value = " + tc.want + "\nCHECK_DEADLOCK TRUE\n"
			if out.Config != want {
				t.Fatalf("config:\n%s\nwant:\n%s", out.Config, want)
			}
		})
	}
}

func TestFullModuleDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"missing separator", "module M { var x def Init = true; }", "expected \";\""},
		{"unclosed module", "module M { def X = true;", "expected \"}\""},
		{"unclosed comment", "module M { /*", "unterminated block comment"},
		{"unclosed string", "module M { def X = \"text; }", "unterminated string"},
		{"unknown quoted operator", "module M { def X = `???`; }", "unknown TLA+ operator"},
		{"keyword identifier", "module M { var next; }", "expected an identifier"},
		{"empty operator declaration", "module M { const F(); }", "at least one placeholder"},
		{"local variable", "module M { local var x; }", "local applies only"},
		{"invalid prefix fixity", "module M { def prefix `+`(x) = x; }", "not a TLA+ prefix"},
		{"invalid postfix fixity", "module M { const postfix `+`; }", "not a TLA+ postfix"},
		{"hide only", "module M { hide only true; }", "hide does not accept only"},
		{"multiple roots", "module M {} module N {}", "only one root module"},
		{"duplicate config", "module M { config {} config {} }", "only one config"},
		{"unknown config", "module M { config { typo X; } }", "unknown TLC config"},
		{"mixed behavior config", "module M { config { specification Spec; init Init; next Next; } }", "choose specification or init/next"},
		{"incomplete behavior config", "module M { config { init Init; } }", "requires both init and next"},
		{"invalid deadlock value", "module M { config { checkDeadlock 1; } }", "requires true or false"},
		{"duplicate specification", "module M { config { specification A; specification B; } }", "duplicate config"},
		{"computed constant config", "module M { config { constant N = 1 + 1; } }", "expected \";\""},
		{"negative constant config", "module M { config { constant N = -1; } }", "require a definition override"},
		{"nested config", "module M { module N { config {} } }", "root module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compiler.Compile("bad.tlapr", []byte(tc.source), compiler.Options{})
			diagnostic, ok := errors.AsType[*compiler.Diagnostic](err)
			if !ok || diagnostic.Filename != "bad.tlapr" || diagnostic.Line < 1 || diagnostic.Column < 1 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want diagnostic containing %q", err, tc.want)
			}
		})
	}
}

func TestFullModuleConfigOptionalAndDeadlockOverride(t *testing.T) {
	out, err := compiler.Compile("proof.tlapr", []byte("module ProofOnly { theorem Identity = true; proof obvious; }"), compiler.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Config != "" {
		t.Fatalf("proof-only module should not imply a model: %q", out.Config)
	}
	for _, explicit := range []string{"", "checkDeadlock true;", "checkDeadlock false;"} {
		source := []byte("module M { config { specification Spec; " + explicit + " } }")
		out, err := compiler.Compile("m.tlapr", source, compiler.Options{AllowDeadlock: true})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(out.Config, "CHECK_DEADLOCK") != 1 || !strings.Contains(out.Config, "CHECK_DEADLOCK FALSE\n") {
			t.Fatalf("override: %s", out.Config)
		}
	}
}

func TestFullModuleNewlineDiagnostics(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		source := strings.Join([]string{"// heading", "module M {", "  var next;", "}"}, newline)
		_, err := compiler.Compile("newlines.tlapr", []byte(source), compiler.Options{})
		d, ok := errors.AsType[*compiler.Diagnostic](err)
		if !ok || d.Line != 3 || d.Column != 7 {
			t.Errorf("newline=%q diagnostic=%v", newline, err)
		}
	}
}

func TestFullModuleQuotedKeywords(t *testing.T) {
	for _, body := range []string{
		"const `infix`, `postfix`; def `prefix` = true;",
		"def `instance` = true; def Value = `instance`;",
		"def Apply(`infix`(_), `prefix`) = `infix`(`prefix`);",
		"recursive `prefix`(_); def `prefix`(n) = if n == 0 then 0 else `prefix`(0);",
		"theorem true; proof { step: define { def `postfix` = true; }; qed by `postfix`; }",
	} {
		if _, err := compiler.Compile("quoted.tlapr", []byte("module Quoted {"+body+"}"), compiler.Options{}); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
}

func TestFullModuleOperatorSourceMap(t *testing.T) {
	source := "module Operators {\n  const infix `++`, prefix `-`, postfix `^+`;\n  recursive F(_);\n  def F(x) = x;\n  def infix `**`(left, right) = left;\n}"
	out, err := compiler.Compile("operators.tlapr", []byte(source), compiler.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"++", "-.", "^+", "F", "F", "**"}
	if len(out.Symbols) != len(want) {
		t.Fatalf("symbols = %+v", out.Symbols)
	}
	lines := strings.Split(out.TLA, "\n")
	for i, symbol := range out.Symbols {
		if symbol.Generated != want[i] || !strings.Contains(lines[symbol.GeneratedLine-1], symbol.Generated) {
			t.Errorf("symbol %d = %+v; generated line: %s", i, symbol, lines[symbol.GeneratedLine-1])
		}
	}
	if out.Symbols[1].Name != "-" || out.Symbols[0].Line != 2 || out.Symbols[0].Column != 15 {
		t.Fatalf("source spelling or position was lost: %+v", out.Symbols[:3])
	}
}
