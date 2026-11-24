package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/s4na/tlapr/compiler"
)

const inventorySource = `model Inventory {
  state {
    stock: Int = 2
  }
  action purchase {
    require stock > 0
    stock -= 1
  }
  invariant nonnegative { stock >= 0 }
}
`

// Reuse the test binary as a portable stand-in for java. Real TLC integration
// is covered separately; these cases exercise process handling by the CLI.
func TestMain(m *testing.M) {
	if mode := os.Getenv("TLAPR_TEST_JAVA_MODE"); mode != "" && len(os.Args) > 1 && os.Args[1] == "-XX:+UseParallelGC" {
		if len(os.Args) < 7 || !strings.HasPrefix(os.Args[2], "-DTLA-Library=") ||
			!filepath.IsAbs(strings.TrimPrefix(os.Args[2], "-DTLA-Library=")) || !filepath.IsAbs(os.Args[4]) {
			fmt.Fprintln(os.Stderr, "unexpected Java invocation")
			os.Exit(99)
		}
		module := strings.TrimSuffix(os.Args[len(os.Args)-1], ".tla")
		for _, path := range []string{module + ".cfg", module + ".tla", module + ".map.json"} {
			if _, err := os.Stat(path); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(99)
			}
		}
		if os.Args[3] == "-cp" {
			if len(os.Args) != 7 || os.Args[5] != "tla2sany.SANY" {
				fmt.Fprintln(os.Stderr, "unexpected SANY invocation")
				os.Exit(99)
			}
			if mode == "sany-empty" {
				os.Exit(0)
			}
			fmt.Fprintln(os.Stdout, "Semantic processing of module "+module)
			switch mode {
			case "sany-semantic", "sany-level":
				fmt.Fprintln(os.Stdout, "Semantic errors:\n\n*** Errors: 1\n\ninvalid expression")
			case "sany-abort":
				fmt.Fprintln(os.Stderr, "*** Abort messages: 1\n\naborted")
			case "sany-warnings":
				fmt.Fprintln(os.Stdout, "Semantic errors:\n\n*** Warnings: 1\n\nwarning only")
			case "sany-crash":
				os.Exit(17)
			case "timeout":
				time.Sleep(time.Minute)
			}
			os.Exit(0)
		}
		if len(os.Args) != 10 || os.Args[3] != "-jar" ||
			os.Args[5] != "-workers" || os.Args[6] != "1" || os.Args[7] != "-config" {
			fmt.Fprintln(os.Stderr, "unexpected TLC invocation")
			os.Exit(99)
		}
		switch mode {
		case "failure":
			fmt.Fprintln(os.Stdout, "Error: Invariant Invariant_nonnegative is violated.")
			fmt.Fprintln(os.Stdout, "State 4: <Action_purchase line 12, col 1>")
			fmt.Fprintln(os.Stdout, "/\\ v_stock = -1")
			os.Exit(12)
		case "timeout":
			time.Sleep(time.Minute)
		case "allow-deadlock":
			cfg, err := os.ReadFile(os.Args[8])
			if err != nil || !strings.Contains(string(cfg), "CHECK_DEADLOCK FALSE") {
				fmt.Fprintln(os.Stderr, "deadlock configuration was not passed to TLC")
				os.Exit(99)
			}
		}
		fmt.Fprintln(os.Stdout, "Model checking completed. No error has been found.")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func writeModel(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.tlapr")
	if err := os.WriteFile(path, []byte(inventorySource), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCompileWritesSpecificationConfigAndSourceMap(t *testing.T) {
	source := writeModel(t)
	outDir := filepath.Join(t.TempDir(), "generated")
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"compile", "-o", outDir, "-allow-deadlock", source}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	for _, ext := range []string{".tla", ".cfg", ".map.json"} {
		path := filepath.Join(outDir, "Tlapr_Inventory"+ext)
		if !strings.Contains(stdout.String(), path) {
			t.Errorf("output does not name %s: %s", path, &stdout)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error(err)
		}
	}
	cfg, err := os.ReadFile(filepath.Join(outDir, "Tlapr_Inventory.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "CHECK_DEADLOCK FALSE") {
		t.Errorf("missing explicit deadlock option: %s", cfg)
	}
	mapData, err := os.ReadFile(filepath.Join(outDir, "Tlapr_Inventory.map.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sourceMap struct {
		Source  string            `json:"source"`
		Module  string            `json:"module"`
		Symbols []compiler.Symbol `json:"symbols"`
	}
	if err := json.Unmarshal(mapData, &sourceMap); err != nil {
		t.Fatal(err)
	}
	if sourceMap.Source != source || sourceMap.Module != "Tlapr_Inventory" || len(sourceMap.Symbols) != 3 {
		t.Errorf("unexpected source map: %+v", sourceMap)
	}
}

func TestCompilePreservesSourceFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "Tlapr_Inventory.tla")
	if err := os.WriteFile(source, []byte(inventorySource), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run(t.Context(), []string{"compile", "-o", dir, source}, io.Discard, &stderr); code != 1 {
		t.Fatalf("exit = %d, want 1; %s", code, &stderr)
	}
	got, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != inventorySource {
		t.Fatal("compile overwrote its source")
	}
}

func TestCompileReportsInvalidModelAndWriteFailure(t *testing.T) {
	t.Run("invalid model", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "invalid.tlapr")
		if err := os.WriteFile(source, []byte("model {"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "out")
		var stderr bytes.Buffer
		if code := run(t.Context(), []string{"compile", "-o", out, source}, io.Discard, &stderr); code != 1 {
			t.Fatalf("exit = %d, want 1; %s", code, &stderr)
		}
		if !strings.Contains(stderr.String(), source) {
			t.Errorf("error omits source: %s", &stderr)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("created output for invalid model: %v", err)
		}
	})
	t.Run("output path is a file", func(t *testing.T) {
		source := writeModel(t)
		var stderr bytes.Buffer
		if code := run(t.Context(), []string{"compile", "-o", source, source}, io.Discard, &stderr); code != 1 {
			t.Fatalf("exit = %d, want 1; %s", code, &stderr)
		}
	})
}

func TestUsageErrors(t *testing.T) {
	t.Setenv("TLA2TOOLS_JAR", "")
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"compile"},
		{"compile", "a.tlapr", "b.tlapr"},
		{"compile", "a.tlapr", "-o", "out"},
		{"check", "a.tlapr"},
		{"check", "-jar", "tlc.jar", "-timeout", "0s", "a.tlapr"},
		{"validate", "a.tlapr"},
		{"validate", "-jar", "tlc.jar", "-timeout", "-1s", "a.tlapr"},
		{"validate", "-jar", "tlc.jar", "-allow-deadlock", "a.tlapr"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stderr bytes.Buffer
			if code := run(t.Context(), args, io.Discard, &stderr); code != 2 {
				t.Errorf("exit = %d, want 2; %s", code, &stderr)
			}
		})
	}
	if code := run(t.Context(), []string{"--help"}, io.Discard, io.Discard); code != 0 {
		t.Errorf("help exit = %d, want 0", code)
	}
}

func fakeJava(t *testing.T, mode string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "java"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TLAPR_TEST_JAVA_MODE", mode)
	jar := filepath.Join(t.TempDir(), "tla2tools.jar")
	if err := os.WriteFile(jar, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return jar
}

func TestCheckPreservesTLCOutcomeAndShowsDSLNames(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want int
	}{
		{"success", 0},
		{"failure", 12},
		{"allow-deadlock", 0},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			jar := fakeJava(t, tc.mode)
			source := writeModel(t)
			args := []string{"check", "-jar", jar}
			if tc.mode == "allow-deadlock" {
				t.Setenv("TLA2TOOLS_JAR", jar)
				args = []string{"check", "-allow-deadlock"}
			}
			args = append(args, source)
			var stdout, stderr bytes.Buffer
			if code := run(t.Context(), args, &stdout, &stderr); code != tc.want {
				t.Fatalf("exit = %d, want %d; %s", code, tc.want, &stderr)
			}
			if tc.mode == "failure" {
				for _, want := range []string{"Invariant nonnegative [" + source + ":9:", "purchase [" + source + ":5:", "stock = -1"} {
					if !strings.Contains(stdout.String(), want) {
						t.Errorf("missing %q in counterexample: %s", want, &stdout)
					}
				}
				if strings.Contains(stdout.String(), "v_stock") {
					t.Errorf("counterexample exposes generated state name: %s", &stdout)
				}
			}
		})
	}
}

func TestCheckTimeoutAndCancellationAreNotSuccess(t *testing.T) {
	jar := fakeJava(t, "timeout")
	source := writeModel(t)
	var stderr bytes.Buffer
	if code := run(t.Context(), []string{"check", "-jar", jar, "-timeout", "50ms", source}, io.Discard, &stderr); code != 124 {
		t.Fatalf("timeout exit = %d, want 124; %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "検証は完了していません") {
		t.Errorf("timeout does not report incomplete verification: %s", &stderr)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stderr.Reset()
	if code := run(ctx, []string{"check", "-jar", jar, source}, io.Discard, &stderr); code != 130 {
		t.Fatalf("canceled exit = %d, want 130; %s", code, &stderr)
	}
}

func TestSymbolWriterRespectsBoundariesAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	w := newSymbolWriter(&out, "example.tlapr", []compiler.Symbol{
		{Generated: "v_stock", Name: "stock", Kind: "state"},
		{Generated: "Action_purchase", Name: "purchase", Kind: "action", Line: 5, Column: 3},
	})
	for _, chunk := range []string{"v_st", "ock = 2; xv_stock v_stock2 ", strings.Repeat("x", 10000), " Action_pur", "chase v_stock"} {
		if _, err := io.WriteString(w, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "stock = 2; xv_stock v_stock2 " + strings.Repeat("x", 10000) + " purchase [example.tlapr:5:3] stock"
	if out.String() != want {
		t.Errorf("unexpected streamed translation: %q", out.String())
	}
}
