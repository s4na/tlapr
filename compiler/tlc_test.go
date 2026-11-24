//go:build tlapr_integration

package compiler_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s4na/tlapr/compiler"
)

// 実際のTLCで生成した仕様を検査し、変換による意味の変化を検出する。
func TestTLC(t *testing.T) {
	jar := requireTLC(t)
	inventory, err := os.ReadFile("../examples/inventory.tlapr")
	if err != nil {
		t.Fatal(err)
	}
	bug, err := os.ReadFile("../examples/inventory_bug.tlapr")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		source        string
		allowDeadlock bool
		wantExit      int
		wantOutput    []string
	}{
		{
			name:          "inventory_and_unchanged_state",
			source:        string(inventory),
			allowDeadlock: true,
			wantOutput:    []string{"Model checking completed. No error has been found.", "11 distinct states found"},
		},
		{
			name:       "terminal_state_is_a_deadlock_by_default",
			source:     string(inventory),
			wantExit:   11,
			wantOutput: []string{"Deadlock reached.", "v_stock = 0", "v_sold = 10"},
		},
		{
			name:          "missing_guard_violates_invariant",
			source:        string(bug),
			allowDeadlock: true,
			wantExit:      12,
			wantOutput:    []string{"Invariant Invariant_nonnegative is violated.", "v_stock = -1"},
		},
		{
			name: "sequential_assignments_are_one_atomic_transition",
			source: `model Sequential {
  state { x: Int = 0; y: Int = 0; stopped: Bool = false; untouched: Bool = true }
  action update {
    require !stopped
    x += 1
    x += 1
    y = x
    stopped = true
  }
  invariant atomic { !stopped || (x == 2 && y == 2 && untouched) }
  invariant no_intermediate_state { x == 0 || x == 2 }
}`,
			allowDeadlock: true,
			wantOutput:    []string{"Model checking completed. No error has been found.", "2 distinct states found"},
		},
		{
			name: "all_enabled_actions_are_explored",
			source: `model Branches {
  state { phase: Int = 0 }
  action safe { require phase == 0; phase = 1 }
  action unsafe { require phase == 0; phase = 2 }
  invariant allowed { phase != 2 }
}`,
			allowDeadlock: true,
			wantExit:      12,
			wantOutput:    []string{"Invariant Invariant_allowed is violated.", "v_phase = 2"},
		},
		{
			name: "integer_endpoints",
			source: `model Endpoints {
  state { x: Int = -2147483648 }
  action move { require x < 0; x = 2147483647 }
  invariant endpoints { x == -2147483648 || x == 2147483647 }
}`,
			allowDeadlock: true,
			wantOutput:    []string{"Model checking completed. No error has been found.", "2 distinct states found"},
		},
		{
			name: "initial_state_is_checked",
			source: `model BadInitial {
  state { x: Int = -1 }
  action stay { x = x }
  invariant positive { x >= 0 }
}`,
			wantExit:   12,
			wantOutput: []string{"Invariant Invariant_positive is violated by the initial state"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, code := runTLC(t, jar, test.source, test.allowDeadlock)
			if code != test.wantExit {
				t.Fatalf("TLC exit = %d, want %d\n%s", code, test.wantExit, output)
			}
			for _, want := range test.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("TLC出力に %q がない:\n%s", want, output)
				}
			}
		})
	}
}

func runTLC(t *testing.T, jar, source string, allowDeadlock bool) (string, int) {
	t.Helper()
	result, err := compiler.Compile("test.tlapr", []byte(source), compiler.Options{AllowDeadlock: allowDeadlock})
	if err != nil {
		t.Fatal(err)
	}
	run := runGeneratedTLC(t, jar, result, false)
	return run.log, run.exitCode
}

func requireTLC(t *testing.T) string {
	t.Helper()
	jar := os.Getenv("TLA2TOOLS_JAR")
	if jar == "" {
		t.Fatal("tlapr_integration には TLA2TOOLS_JAR が必要です。統合テストを省略せず失敗します")
	}
	jar, err := filepath.Abs(jar)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(jar); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("TLA2TOOLS_JARが通常ファイルではありません: %s (%v)", jar, err)
	}
	if _, err := exec.LookPath("java"); err != nil {
		t.Fatal(err)
	}
	return jar
}

type tlcRun struct {
	log      string
	exitCode int
	graph    []byte
}

func runGeneratedTLC(t *testing.T, jar string, result *compiler.Output, dumpGraph bool) tlcRun {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		result.Module + ".tla": result.TLA,
		result.Module + ".cfg": result.Config,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	args := []string{"-XX:+UseParallelGC", "-jar", jar, "-workers", "1", "-config", result.Module + ".cfg"}
	if dumpGraph {
		args = append(args, "-dump", "dot,actionlabels", "states.dot")
	}
	cmd := exec.CommandContext(ctx, "java", append(args, result.Module+".tla")...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("TLCが制限時間内に完了しなかった: %v\n%s", ctx.Err(), output)
	}
	run := tlcRun{log: string(output)}
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			run.exitCode = exit.ExitCode()
			return run
		}
		t.Fatalf("TLCを実行できなかった: %v\n%s", err, output)
	}
	// 違反で終了したときのdumpは探索途中なので、完全なグラフとして扱わない。
	if dumpGraph {
		run.graph, err = os.ReadFile(filepath.Join(dir, "states.dot"))
		if err != nil {
			t.Fatal(err)
		}
	}
	return run
}
