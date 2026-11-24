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

compile: TLA+、TLC設定、ソース対応表を出力します（既定: ./out）。
check:   JavaとTLCを使って検査します。jarはTLA2TOOLS_JARでも指定できます。
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
	if command != "compile" && command != "check" {
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
	allowDeadlock := flags.Bool("allow-deadlock", false, "到達した終端状態をエラーにしない（デッドロック検査を無効化）")
	var outDir, jar string
	var timeout time.Duration
	if command == "compile" {
		flags.StringVar(&outDir, "o", "out", "生成ファイルの出力先")
	} else {
		flags.StringVar(&jar, "jar", os.Getenv("TLA2TOOLS_JAR"), "tla2tools.jar のパス")
		flags.DurationVar(&timeout, "timeout", 60*time.Second, "TLCの実行制限時間（正の時間）")
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
	if command == "check" && (jar == "" || timeout <= 0) {
		fmt.Fprintln(stderr, "checkには -jar または TLA2TOOLS_JAR と、正の -timeout が必要です。")
		return 2
	}

	sourcePath := flags.Arg(0)
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	output, err := compiler.Compile(sourcePath, source, compiler.Options{AllowDeadlock: *allowDeadlock})
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
	jar, err := filepath.Abs(jar)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, err := os.Stat(jar)
	if err != nil || !info.Mode().IsRegular() {
		fmt.Fprintf(stderr, "TLCのjarファイルを読み込めません: %s\n", jar)
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
	cmd := exec.CommandContext(ctx, "java", "-XX:+UseParallelGC", "-jar", jar,
		"-workers", "1", "-config", output.Module+".cfg", output.Module+".tla")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	out := newSymbolWriter(stdout, sourcePath, output.Symbols)
	errOut := newSymbolWriter(stderr, sourcePath, output.Symbols)
	cmd.Stdout, cmd.Stderr = out, errOut
	runErr := cmd.Run()
	flushErr := errors.Join(out.Flush(), errOut.Flush())
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "TLCが制限時間 %s を超えました。検証は完了していません。\n", timeout)
			return 124
		}
		fmt.Fprintln(stderr, "TLCの実行を中断しました。検証は完了していません。")
		return 130
	}
	if flushErr != nil {
		fmt.Fprintln(stderr, flushErr)
		return 1
	}
	if runErr != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			fmt.Fprintf(stderr, "TLCは終了コード %d で終了しました。\n", exitErr.ExitCode())
			if exitErr.ExitCode() > 0 {
				return exitErr.ExitCode()
			}
		} else {
			fmt.Fprintf(stderr, "TLCを実行できません: %v\n", runErr)
		}
		return 1
	}
	return 0
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
