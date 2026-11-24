package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/s4na/tlapr/compiler"
)

func TestValidateChecksSANYDiagnosticsAndProcessOutcome(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want int
	}{
		{"sany-success", 0},
		{"sany-warnings", 0},
		{"sany-semantic", 1},
		{"sany-level", 1},
		{"sany-abort", 1},
		{"sany-empty", 1},
		{"sany-crash", 17},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			jar := fakeJava(t, tc.mode)
			source := writeModel(t)
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), []string{"validate", "-jar", jar, source}, &stdout, &stderr)
			if code != tc.want {
				t.Fatalf("validate exit = %d, want %d; stdout=%s stderr=%s", code, tc.want, &stdout, &stderr)
			}
			if success := strings.Contains(stdout.String(), "レベル検査が成功"); success != (tc.want == 0) {
				t.Fatalf("success message = %v; stdout=%s stderr=%s", success, &stdout, &stderr)
			}
		})
	}
}

func TestValidateTimeoutAndCancellation(t *testing.T) {
	jar := fakeJava(t, "timeout")
	source := writeModel(t)
	var stderr bytes.Buffer
	if code := run(t.Context(), []string{"validate", "-jar", jar, "-timeout", "50ms", source}, io.Discard, &stderr); code != 124 {
		t.Fatalf("timeout exit = %d, want 124; %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "SANYが制限時間") {
		t.Fatalf("timeout omits tool name: %s", &stderr)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stderr.Reset()
	if code := run(ctx, []string{"validate", "-jar", jar, source}, io.Discard, &stderr); code != 130 {
		t.Fatalf("canceled exit = %d, want 130; %s", code, &stderr)
	}
}

func TestCheckRequiresConfiguration(t *testing.T) {
	var stderr bytes.Buffer
	if code := check(t.Context(), "proof.tlapr", &compiler.Output{Module: "Proof"}, "", time.Second, io.Discard, &stderr); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "configブロックが必要") {
		t.Fatalf("missing configuration diagnostic: %s", &stderr)
	}
}

func TestSANYStatusTracksCompleteLinesBeforeNameMapping(t *testing.T) {
	for _, tc := range []struct {
		name, log string
		root, bad bool
	}{
		{"success", "Semantic processing of module Root\n", true, false},
		{"other module", "Semantic processing of module Other\n", false, false},
		{"prefix only", "Semantic processing of module RootExtra\n", false, false},
		{"unterminated line", "Semantic processing of module Root", true, false},
		{"crlf", "Semantic processing of module Root\r\n*** Errors: 0\r\n", true, false},
		{"semantic error", "Semantic processing of module Root\n*** Errors: 2\n", true, true},
		{"aborted", "*** Abort messages: 1\n", false, true},
		{"invalid count", "*** Errors: unknown\n", false, true},
		{"parse", "***Parse Error***\n", false, true},
		{"fatal", "Fatal errors in semantic processing of TLA spec Root\n", false, true},
		{"warning", "Semantic errors:\n*** Warnings: 1\n", false, false},
		{"long line", strings.Repeat("x", 10000) + "\nSemantic processing of module Root\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			w := newSANYStatusWriter(&out, "Root")
			for _, b := range []byte(tc.log) {
				if _, err := w.Write([]byte{b}); err != nil {
					t.Fatal(err)
				}
				if len(w.prefix) > w.limit {
					t.Fatal("status reader retained an unbounded diagnostic")
				}
			}
			w.finishLine()
			if w.processedRoot != tc.root || w.failed != tc.bad {
				t.Fatalf("root=%v failed=%v, want root=%v failed=%v", w.processedRoot, w.failed, tc.root, tc.bad)
			}
			if out.String() != tc.log {
				t.Fatal("status reader changed diagnostic output")
			}
		})
	}
}
