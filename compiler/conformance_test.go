//go:build tlapr_integration

package compiler_test

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/s4na/tlapr/compiler"
)

// The expected graphs are hand-written independently of the compiler. Checking
// only DSL invariants could miss a mistranslation shared by updates and assertions.
func TestTLAPRConformance(t *testing.T) {
	jar := requireTLC(t)
	paths, err := filepath.Glob("testdata/conformance/*.tlapr")
	if err != nil || len(paths) == 0 {
		t.Fatalf("conformance fixturesがありません: %v", err)
	}
	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".tlapr"), func(t *testing.T) {
			out := compileFixture(t, path)
			want := readExpectedGraph(t, strings.TrimSuffix(path, ".tlapr")+".json")
			run := runGeneratedTLC(t, jar, out, true)
			if run.exitCode != 0 {
				t.Fatalf("TLC exit = %d\n%s", run.exitCode, run.log)
			}
			got, err := parseStateGraph(run.graph)
			if err != nil {
				t.Fatalf("TLC graph: %v\n%s", err, run.graph)
			}
			if err := graphDifference(got, want); err != nil {
				t.Fatal(err)
			}
			t.Logf("%d状態・%d遷移・%d初期状態が独立した期待値と一致", len(got.states), len(got.transitions), len(got.initial))
		})
	}
}

func TestTLAPRViolations(t *testing.T) {
	jar := requireTLC(t)
	for _, name := range []string{"initial", "transition"} {
		t.Run(name, func(t *testing.T) {
			out := compileFixture(t, "testdata/violations/"+name+".tlapr")
			run := runGeneratedTLC(t, jar, out, false)
			if err := expectedViolation(run); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// These valid but wrong translations still satisfy their DSL invariants. The
// independent graph oracle must reject them, including an extra edge between
// otherwise unchanged states. This checks that the conformance tests can fail.
func TestTLAPRDetectsIncorrectTranslations(t *testing.T) {
	jar := requireTLC(t)
	const cycle = "testdata/conformance/cycle"
	want := readExpectedGraph(t, cycle+".json")
	for _, mutation := range []struct {
		name, before, after string
		sameStates          bool
	}{
		{"wrong_increment", "step1 == (v_n + 1)", "step1 == (v_n + 2)", false},
		{"missing_action", "    \\/ Action_increment\n", "", false},
		{"extra_transition", "/\\ (v_n = 2)", "/\\ (v_n >= 1)", true},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			out := compileFixture(t, cycle+".tlapr")
			out.TLA = replaceExactlyOnce(t, out.TLA, mutation.before, mutation.after)
			run := runGeneratedTLC(t, jar, out, true)
			if run.exitCode != 0 {
				t.Fatalf("mutationはTLC上で成功する有限モデルである必要があります: %d\n%s", run.exitCode, run.log)
			}
			got, err := parseStateGraph(run.graph)
			if err != nil {
				t.Fatal(err)
			}
			if mutation.sameStates && !slices.Equal(got.states, want.states) {
				t.Fatal("extra_transitionは状態集合を変えず、辺だけを増やす必要があります")
			}
			if err := graphDifference(got, want); err == nil {
				t.Fatal("誤った変換を期待値照合が検出できませんでした")
			} else {
				t.Logf("意図した変換ミスを検出: %v", err)
			}
		})
	}
	t.Run("missing_invariant", func(t *testing.T) {
		out := compileFixture(t, "testdata/violations/transition.tlapr")
		out.Config = replaceExactlyOnce(t, out.Config, "INVARIANT Invariant_expected_false\n", "")
		run := runGeneratedTLC(t, jar, out, false)
		if run.exitCode != 0 {
			t.Fatalf("invariant欠落時にはTLCが成功する対照モデルが必要です: %d\n%s", run.exitCode, run.log)
		}
		if err := expectedViolation(run); err == nil {
			t.Fatal("不変条件を出力しない変換ミスを検出できませんでした")
		}
	})
}

func compileFixture(t *testing.T, path string) *compiler.Output {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := compiler.Compile(path, source, compiler.Options{AllowDeadlock: true})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func replaceExactlyOnce(t *testing.T, text, before, after string) string {
	t.Helper()
	if n := strings.Count(text, before); n != 1 {
		t.Fatalf("mutation対象 %q が %d 個です（1個を期待）", before, n)
	}
	return strings.Replace(text, before, after, 1)
}

func expectedViolation(run tlcRun) error {
	if run.exitCode != 12 || !strings.Contains(run.log, "Invariant Invariant_expected_false is violated") {
		return fmt.Errorf("期待した不変条件違反を検出できません: exit=%d\n%s", run.exitCode, run.log)
	}
	return nil
}

type stateGraph struct {
	states, initial, transitions []string
}

func graphDifference(got, want stateGraph) error {
	for _, part := range []struct {
		name      string
		got, want []string
	}{
		{"states", got.states, want.states},
		{"initial", got.initial, want.initial},
		{"transitions", got.transitions, want.transitions},
	} {
		if !slices.Equal(part.got, part.want) {
			return fmt.Errorf("%s differ:\n got: %v\nwant: %v", part.name, part.got, part.want)
		}
	}
	return nil
}

func readExpectedGraph(t *testing.T, path string) stateGraph {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		States      []map[string]any `json:"states"`
		Initial     []int            `json:"initial"`
		Transitions [][2]int         `json:"transitions"`
	}
	if err := json.Unmarshal(data, &expected, json.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
	if len(expected.States) == 0 || len(expected.Initial) == 0 {
		t.Fatal("独立した期待値には状態と初期状態を明示してください")
	}
	var graph stateGraph
	for _, values := range expected.States {
		state, err := canonicalState(values)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(graph.states, state) {
			t.Fatal("期待する状態が重複しています")
		}
		graph.states = append(graph.states, state)
	}
	stateAt := func(index int) string {
		if index < 0 || index >= len(graph.states) {
			t.Fatalf("期待値の状態indexが範囲外です: %d", index)
		}
		return graph.states[index]
	}
	for _, index := range expected.Initial {
		graph.initial = append(graph.initial, stateAt(index))
	}
	for _, edge := range expected.Transitions {
		graph.transitions = append(graph.transitions, stateAt(edge[0])+" -> "+stateAt(edge[1]))
	}
	return normalizeGraph(graph)
}

func canonicalState(values map[string]any) (string, error) {
	if len(values) == 0 {
		return "", fmt.Errorf("empty state")
	}
	data, err := json.Marshal(values, json.Deterministic(true))
	return string(data), err
}

func normalizeGraph(graph stateGraph) stateGraph {
	slices.Sort(graph.states)
	slices.Sort(graph.initial)
	slices.Sort(graph.transitions)
	graph.states = slices.Compact(graph.states)
	graph.initial = slices.Compact(graph.initial)
	graph.transitions = slices.Compact(graph.transitions)
	return graph
}

func TestTLAPRGraphReader(t *testing.T) {
	// IDs, variable order, edge order, and duplicate edges must not change the
	// semantic comparison. Both values and the initial-state marker must remain.
	const dot = `strict digraph DiskGraph {
nodesep=0.35;
subgraph cluster_graph {
color="white";
-9 [label="/\\ v_x = 0\n/\\ v_ready = FALSE",style = filled]
-9 -> 42 [label="Action_go"];
42 [label="/\\ v_ready = TRUE\n/\\ v_x = 1"];
-9 -> 42 [label="Action_also_go"];
42 -> -9 [label="Action_back"];
{rank = same; -9;}
{rank = same; 42;}
}
}`
	start := `{"ready":false,"x":0}`
	end := `{"ready":true,"x":1}`
	want := normalizeGraph(stateGraph{
		states: []string{start, end}, initial: []string{start},
		transitions: []string{start + " -> " + end, end + " -> " + start},
	})
	got, err := parseStateGraph([]byte(dot))
	if err != nil {
		t.Fatal(err)
	}
	if err := graphDifference(got, want); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct{ name, source string }{
		{"truncated", strings.TrimSuffix(dot, "\n}")},
		{"missing initial marker", strings.ReplaceAll(dot, ",style = filled", "")},
		{"unknown value", strings.Replace(dot, "v_x = 1", "v_x = undefined", 1)},
		{"unknown variable", strings.Replace(dot, "v_x = 1", "x = 1", 1)},
		{"missing node", strings.Replace(dot, "42 -> -9", "42 -> 999", 1)},
		{"unknown format", strings.Replace(dot, "nodesep=0.35;", "unknown=true;", 1)},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := parseStateGraph([]byte(invalid.source)); err == nil {
				t.Fatal("壊れた/未対応のグラフを読み飛ばしてはいけません")
			}
		})
	}
}

var (
	dotNode = regexp.MustCompile(`^(-?[0-9]+) \[label=("(?:[^"\\]|\\.)*")(.*)\];?$`)
	dotEdge = regexp.MustCompile(`^(-?[0-9]+) -> (-?[0-9]+) \[.*\];$`)
)

// This deliberately reads only the DOT dialect emitted by pinned TLC v1.7.4
// for Int/Bool models. Unknown lines/values are failures, not silently skipped.
func parseStateGraph(data []byte) (stateGraph, error) {
	var graph stateGraph
	states := make(map[string]string)
	var edges [][2]string
	initial := make(map[string]bool)
	text := strings.TrimSpace(string(data))
	if !strings.HasPrefix(text, "strict digraph DiskGraph {\n") || !strings.HasSuffix(text, "\n}\n}") {
		return graph, fmt.Errorf("incomplete or unsupported TLC DOT graph")
	}
	for line := range strings.SplitSeq(text, "\n") {
		if match := dotNode.FindStringSubmatch(line); match != nil {
			label, err := strconv.Unquote(match[2])
			if err != nil {
				return graph, err
			}
			values := make(map[string]any)
			for assignment := range strings.SplitSeq(label, "\n") {
				assignment = strings.TrimPrefix(assignment, "/\\ ")
				name, value, ok := strings.Cut(assignment, " = ")
				if !ok || !strings.HasPrefix(name, "v_") {
					return graph, fmt.Errorf("unsupported state assignment: %q", assignment)
				}
				name = strings.TrimPrefix(name, "v_")
				if _, duplicate := values[name]; duplicate {
					return graph, fmt.Errorf("duplicate state variable: %s", name)
				}
				switch value {
				case "TRUE", "FALSE":
					values[name] = value == "TRUE"
				default:
					n, err := strconv.ParseInt(value, 10, 32)
					if err != nil {
						return graph, fmt.Errorf("unsupported state value: %q", value)
					}
					values[name] = n
				}
			}
			state, err := canonicalState(values)
			if err != nil {
				return graph, err
			}
			if previous, exists := states[match[1]]; exists && previous != state {
				return graph, fmt.Errorf("state fingerprint has conflicting values: %s", match[1])
			}
			states[match[1]] = state
			if strings.Contains(match[3], "style = filled") {
				initial[match[1]] = true
			}
			continue
		}
		if match := dotEdge.FindStringSubmatch(line); match != nil {
			edges = append(edges, [2]string{match[1], match[2]})
			continue
		}
		switch line {
		case "strict digraph DiskGraph {", "nodesep=0.35;", "subgraph cluster_graph {", `color="white";`, "}":
			continue
		}
		if strings.HasPrefix(line, "{rank = same; ") && strings.HasSuffix(line, "}") {
			continue
		}
		return graph, fmt.Errorf("unsupported DOT line: %q", line)
	}
	if len(states) == 0 || len(initial) == 0 {
		return graph, fmt.Errorf("graph must contain states and an initial state")
	}
	graph.states = slices.Collect(maps.Values(states))
	for id := range initial {
		graph.initial = append(graph.initial, states[id])
	}
	for _, edge := range edges {
		from, fromExists := states[edge[0]]
		to, toExists := states[edge[1]]
		if !fromExists || !toExists {
			return graph, fmt.Errorf("edge references an unknown state: %v", edge)
		}
		graph.transitions = append(graph.transitions, from+" -> "+to)
	}
	return normalizeGraph(graph), nil
}
