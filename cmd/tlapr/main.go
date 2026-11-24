package main

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/s4na/tlapr/compiler"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `使い方:
  tlapr compile [-o DIR] [-allow-deadlock] FILE
  tlapr check [-jar PATH] [-timeout 60s] [-allow-deadlock] FILE
  tlapr validate [-jar PATH] [-timeout 60s] FILE

compile: TLA+、TLC設定、ソース対応表を出力します（既定: ./out）。
check:   JavaとTLCを使って検査します。jarはTLA2TOOLS_JARでも指定できます。
validate: SANYで構文・名前・式のレベルを検査します。証明内容の正しさは検証しません。
checkとvalidateは入力ファイルと同じディレクトリのTLA+モジュールも参照できます。
フラグは入力ファイルより前に指定してください。`)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stdout)
		return 0
	}
	command := args[0]
	if command != "compile" && command != "check" && command != "validate" {
		fmt.Fprintf(stderr, "不明なコマンド: %s\n", command)
		usage(stderr)
		return 2
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		usage(stderr)
		flags.PrintDefaults()
	}
	var allowDeadlock bool
	if command != "validate" {
		flags.BoolVar(&allowDeadlock, "allow-deadlock", false, "到達した終端状態をエラーにしない（デッドロック検査を無効化）")
	}
	var outDir, jar string
	var timeout time.Duration
	if command == "compile" {
		flags.StringVar(&outDir, "o", "out", "生成ファイルの出力先")
	} else {
		flags.StringVar(&jar, "jar", os.Getenv("TLA2TOOLS_JAR"), "tla2tools.jar のパス")
		flags.DurationVar(&timeout, "timeout", 60*time.Second, "検査の実行制限時間（正の時間）")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "入力ファイルを1つ指定してください。フラグは入力ファイルより前に置きます。")
		return 2
	}
	if command != "compile" && (jar == "" || timeout <= 0) {
		fmt.Fprintf(stderr, "%sには -jar または TLA2TOOLS_JAR と、正の -timeout が必要です。\n", command)
		return 2
	}

	sourcePath := flags.Arg(0)
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	output, err := compiler.Compile(sourcePath, source, compiler.Options{AllowDeadlock: allowDeadlock})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if command == "compile" {
		paths, err := writeOutput(outDir, sourcePath, output)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, path := range paths {
			if _, err := fmt.Fprintln(stdout, path); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		return 0
	}
	if command == "validate" {
		return validate(ctx, sourcePath, output, jar, timeout, stdout, stderr)
	}
	return check(ctx, sourcePath, output, jar, timeout, stdout, stderr)
}

func writeOutput(dir, sourcePath string, output *compiler.Output) ([]string, error) {
	data, err := json.Marshal(&struct {
		Source  string            `json:"source"`
		Module  string            `json:"module"`
		Symbols []compiler.Symbol `json:"symbols"`
	}{sourcePath, output.Module, output.Symbols}, jsontext.WithIndent("  "))
	if err != nil {
		return nil, fmt.Errorf("ソース対応表を生成できません: %w", err)
	}
	files := []struct {
		extension string
		data      []byte
	}{
		{".tla", []byte(output.TLA)},
		{".cfg", []byte(output.Config)},
		{".map.json", append(data, '\n')},
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = filepath.Join(dir, output.Module+file.extension)
		info, err := os.Stat(paths[i])
		if err == nil && os.SameFile(sourceInfo, info) {
			return nil, fmt.Errorf("入力ファイルを生成結果で上書きできません: %s", paths[i])
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for i, file := range files {
		if err := os.WriteFile(paths[i], file.data, 0o644); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func check(ctx context.Context, sourcePath string, output *compiler.Output, jar string, timeout time.Duration, stdout, stderr io.Writer) int {
	if strings.TrimSpace(output.Config) == "" {
		fmt.Fprintln(stderr, "checkにはTLCのconfigブロックが必要です。構文・意味の検査にはvalidateを使ってください。")
		return 1
	}
	return runJavaTool(ctx, sourcePath, output, jar, timeout, false, stdout, stderr)
}

func validate(ctx context.Context, sourcePath string, output *compiler.Output, jar string, timeout time.Duration, stdout, stderr io.Writer) int {
	return runJavaTool(ctx, sourcePath, output, jar, timeout, true, stdout, stderr)
}

func runJavaTool(ctx context.Context, sourcePath string, output *compiler.Output, jar string, timeout time.Duration, validation bool, stdout, stderr io.Writer) int {
	tool := "TLC"
	if validation {
		tool = "SANY"
	}
	jar, err := filepath.Abs(jar)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, err := os.Stat(jar)
	if err != nil || !info.Mode().IsRegular() {
		fmt.Fprintf(stderr, "%sのjarファイルを読み込めません: %s\n", tool, jar)
		return 1
	}
	sourceDir, err := filepath.Abs(filepath.Dir(sourcePath))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	dir, err := os.MkdirTemp("", "tlapr-*")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	if _, err := writeOutput(dir, sourcePath, output); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"-XX:+UseParallelGC", "-DTLA-Library=" + sourceDir}
	if validation {
		args = append(args, "-cp", jar, "tla2sany.SANY", output.Module+".tla")
	} else {
		args = append(args, "-jar", jar, "-workers", "1", "-config", output.Module+".cfg", output.Module+".tla")
	}
	cmd := exec.CommandContext(ctx, "java", args...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	out := newSymbolWriter(stdout, sourcePath, output.Symbols)
	errOut := newSymbolWriter(stderr, sourcePath, output.Symbols)
	cmd.Stdout, cmd.Stderr = out, errOut
	var sanyOut, sanyErr *sanyStatusWriter
	if validation {
		sanyOut = newSANYStatusWriter(out, output.Module)
		sanyErr = newSANYStatusWriter(errOut, output.Module)
		cmd.Stdout, cmd.Stderr = sanyOut, sanyErr
	}
	runErr := cmd.Run()
	flushErr := errors.Join(out.Flush(), errOut.Flush())
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "%sが制限時間 %s を超えました。検証は完了していません。\n", tool, timeout)
			return 124
		}
		fmt.Fprintf(stderr, "%sの実行を中断しました。検証は完了していません。\n", tool)
		return 130
	}
	if flushErr != nil {
		fmt.Fprintln(stderr, flushErr)
		return 1
	}
	if runErr != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			fmt.Fprintf(stderr, "%sは終了コード %d で終了しました。\n", tool, exitErr.ExitCode())
			if exitErr.ExitCode() > 0 {
				return exitErr.ExitCode()
			}
		} else {
			fmt.Fprintf(stderr, "%sを実行できません: %v\n", tool, runErr)
		}
		return 1
	}
	if validation {
		sanyOut.finishLine()
		sanyErr.finishLine()
		if sanyOut.failed || sanyErr.failed || !(sanyOut.processedRoot || sanyErr.processedRoot) {
			fmt.Fprintln(stderr, "SANYがエラーを報告したか、対象モジュールの意味解析を確認できません。検証は成功していません。")
			return 1
		}
		if _, err := fmt.Fprintln(stdout, "SANYによる構文・名前・式のレベル検査が成功しました。"); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}

// The SANY entry point in tla2tools 1.7.4 exits zero for semantic and level
// errors. Track its diagnostics as well as the process exit code. Requiring
// the root's semantic-processing message also rejects an empty/partial run.
// Keep only a bounded line prefix; even very large diagnostics are streamed.
type sanyStatusWriter struct {
	out           io.Writer
	rootLine      string
	prefix        []byte
	limit         int
	overflow      bool
	processedRoot bool
	failed        bool
}

func newSANYStatusWriter(out io.Writer, module string) *sanyStatusWriter {
	rootLine := "Semantic processing of module " + module
	return &sanyStatusWriter{out: out, rootLine: rootLine, limit: max(256, len(rootLine)+1)}
}

func (w *sanyStatusWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	for _, b := range p[:n] {
		if b == '\n' {
			w.finishLine()
		} else if len(w.prefix) < w.limit {
			w.prefix = append(w.prefix, b)
		} else {
			w.overflow = true
		}
	}
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func (w *sanyStatusWriter) finishLine() {
	line := strings.TrimSuffix(string(w.prefix), "\r")
	if !w.overflow && line == w.rootLine {
		w.processedRoot = true
	}
	for _, marker := range []string{"*** Errors:", "*** Abort messages:"} {
		if number, found := strings.CutPrefix(line, marker); found {
			count, err := strconv.Atoi(strings.TrimSpace(number))
			if err != nil || count != 0 || w.overflow {
				w.failed = true
			}
		}
	}
	if strings.HasPrefix(line, "***Parse Error***") || strings.HasPrefix(line, "Fatal errors") {
		w.failed = true
	}
	w.prefix = w.prefix[:0]
	w.overflow = false
}

// symbolWriter retains only an incomplete identifier, so counterexample traces
// can be streamed without collecting the complete TLC output in memory.
type symbolWriter struct {
	out          io.Writer
	replacements map[string]string
	maxLength    int
	pending      []byte
	passthrough  bool
}

func newSymbolWriter(out io.Writer, sourcePath string, symbols []compiler.Symbol) *symbolWriter {
	w := &symbolWriter{out: out, replacements: make(map[string]string)}
	for _, symbol := range symbols {
		replacement := symbol.Name
		if symbol.Kind == "action" || symbol.Kind == "invariant" {
			replacement = fmt.Sprintf("%s [%s:%d:%d]", symbol.Name, sourcePath, symbol.Line, symbol.Column)
		}
		w.replacements[symbol.Generated] = replacement
		w.maxLength = max(w.maxLength, len(symbol.Generated))
	}
	return w
}

func (w *symbolWriter) Write(p []byte) (int, error) {
	var output []byte
	for _, b := range p {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' {
			if w.passthrough {
				output = append(output, b)
				continue
			}
			w.pending = append(w.pending, b)
			if len(w.pending) > w.maxLength {
				output = append(output, w.pending...)
				w.pending = w.pending[:0]
				w.passthrough = true
			}
			continue
		}
		output = w.appendPending(output)
		w.passthrough = false
		output = append(output, b)
	}
	if len(output) > 0 {
		n, err := w.out.Write(output)
		if err != nil {
			return len(p), err
		}
		if n != len(output) {
			return len(p), io.ErrShortWrite
		}
	}
	return len(p), nil
}

func (w *symbolWriter) appendPending(output []byte) []byte {
	if replacement, ok := w.replacements[string(w.pending)]; ok {
		output = append(output, replacement...)
	} else {
		output = append(output, w.pending...)
	}
	w.pending = w.pending[:0]
	return output
}

func (w *symbolWriter) Flush() error {
	pending := w.appendPending(nil)
	if len(pending) == 0 {
		return nil
	}
	n, err := w.out.Write(pending)
	if err == nil && n != len(pending) {
		return io.ErrShortWrite
	}
	return err
}
