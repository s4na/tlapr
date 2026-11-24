//go:build tlapr_integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s4na/tlapr/compiler"
)

func validationJar(t *testing.T) string {
	t.Helper()
	jar := os.Getenv("TLA2TOOLS_JAR")
	if jar == "" {
		t.Fatal("tlapr_integrationにはTLA2TOOLS_JARが必要です")
	}
	jar, err := filepath.Abs(jar)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jar); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("java"); err != nil {
		t.Fatal(err)
	}
	return jar
}

// Keep documented grammar/tool discrepancies visible and ensure the public
// command reports failure rather than certifying a specification SANY rejects.
func TestTLAPRValidateKnownToolLimits(t *testing.T) {
	jar := validationJar(t)
	paths, err := filepath.Glob("../../compiler/testdata/tool_limits/*.tlapr")
	if err != nil || len(paths) == 0 {
		t.Fatalf("tool-limit fixtures: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := compiler.Compile(path, source, compiler.Options{}); err != nil {
				t.Fatalf("the documented grammar must compile: %v", err)
			}
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), []string{"validate", "-jar", jar, path}, &stdout, &stderr)
			log := stdout.String() + stderr.String()
			if code == 0 || !strings.Contains(log, "Parse Error") || strings.Contains(log, "レベル検査が成功") {
				t.Fatalf("documented SANY rejection changed: exit=%d\n%s", code, log)
			}
		})
	}
}

// Exercise the real SANY entry point: semantic and level errors in the pinned
// version exit zero. A process-only test would incorrectly certify these cases.
func TestTLAPRValidateSANY(t *testing.T) {
	jar := validationJar(t)
	for _, tc := range []struct {
		name, body, diagnostic string
		valid                  bool
	}{
		{"Valid", "VARIABLE x\nInit == x = 0\n", "Semantic processing of module Valid", true},
		{"Proof", "THEOREM Identity == TRUE\nPROOF OBVIOUS\n", "Semantic processing of module Proof", true},
		{"BadName", "Value == unknown\n", "Unknown operator", false},
		{"BadLevel", "VARIABLE x\nDouble == (x')'\n", "Level error", false},
		{"BadParse", "Value == (\n", "Parse Error", false},
		{"MissingModule", "EXTENDS MissingDependency\n", "Cannot find source file for module MissingDependency", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "input.tlapr")
			if err := os.WriteFile(source, []byte("// test input\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out := &compiler.Output{Module: tc.name, TLA: "---- MODULE " + tc.name + " ----\n" + tc.body + "====\n"}
			var stdout, stderr bytes.Buffer
			code := validate(t.Context(), source, out, jar, 30*time.Second, &stdout, &stderr)
			if (code == 0) != tc.valid {
				t.Fatalf("validate exit=%d, valid=%v\n%s\n%s", code, tc.valid, &stdout, &stderr)
			}
			if log := stdout.String() + stderr.String(); !strings.Contains(log, tc.diagnostic) {
				t.Fatalf("missing %q:\n%s", tc.diagnostic, log)
			}
			if !tc.valid && strings.Contains(stdout.String(), "レベル検査が成功") {
				t.Fatal("invalid specification reported success")
			}
		})
	}
	t.Run("existing model CLI", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := run(t.Context(), []string{"validate", "-jar", jar, writeModel(t)}, &stdout, &stderr); code != 0 {
			t.Fatalf("validate exit=%d\n%s\n%s", code, &stdout, &stderr)
		}
	})
}

func TestTLAPRToolsResolveSiblingModules(t *testing.T) {
	jar := validationJar(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "counter.tlapr")
	if err := os.WriteFile(source, []byte("// test input\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Helper.tla"), []byte("---- MODULE Helper ----\nLimit == 1\n====\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &compiler.Output{
		Module: "Counter",
		TLA: `---- MODULE Counter ----
EXTENDS Integers, Helper
VARIABLE x
Init == x = 0
Next == x' = IF x = 0 THEN Limit ELSE 0
Spec == Init /\ [][Next]_x
====
`,
		Config: "SPECIFICATION Spec\n",
	}
	for _, validation := range []bool{true, false} {
		name := "check"
		if validation {
			name = "validate"
		}
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runJavaTool(t.Context(), source, out, jar, 30*time.Second, validation, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("%s exit=%d\n%s\n%s", name, code, &stdout, &stderr)
			}
			if !strings.Contains(stdout.String(), "Semantic processing of module Helper") {
				t.Fatalf("sibling module was not processed:\n%s", &stdout)
			}
		})
	}
}
