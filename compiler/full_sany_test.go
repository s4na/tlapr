//go:build tlapr_integration

package compiler_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s4na/tlapr/compiler"
)

// SANY is the independent parser, name resolver and level checker. It checks
// every full-language fixture, including constructs TLC cannot evaluate and
// structured proofs. This test does not claim to discharge proof obligations.
func TestTLAPRFullSyntaxSANY(t *testing.T) {
	jar := requireTLC(t)
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
			dir := t.TempDir()
			file := filepath.Join(dir, out.Module+".tla")
			if err := os.WriteFile(file, []byte(out.TLA), 0o644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "java", "-cp", jar, "tla2sany.SANY", file)
			cmd.Dir = dir
			cmd.WaitDelay = time.Second
			log, err := cmd.CombinedOutput()
			text := string(log)
			if err != nil || ctx.Err() != nil || !strings.Contains(text, "Semantic processing of module "+out.Module+"\n") || strings.Contains(text, "*** Errors:") || strings.Contains(text, "*** Abort messages:") || strings.Contains(text, "Parse Error") || strings.Contains(text, "Fatal errors") {
				t.Fatalf("SANY failed for %s: %v / %v\n%s", path, err, ctx.Err(), text)
			}
		})
	}
}

func TestTLAPRFullQueueExample(t *testing.T) {
	jar := requireTLC(t)
	source, err := os.ReadFile("../examples/queue.tlapr")
	if err != nil {
		t.Fatal(err)
	}
	out, err := compiler.Compile("queue.tlapr", source, compiler.Options{})
	if err != nil {
		t.Fatal(err)
	}
	run := runGeneratedTLC(t, jar, out, false)
	if run.exitCode != 0 || !strings.Contains(run.log, "10 distinct states found") {
		t.Fatalf("queue model failed: exit=%d\n%s", run.exitCode, run.log)
	}
}
