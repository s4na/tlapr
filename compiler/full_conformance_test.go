//go:build tlapr_integration

package compiler_test

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// These expected values and edges are hand-written from each model's meaning.
// TLC's pinned finite-value renderer gives sets, records, functions and tuples a
// stable representation. Only whitespace outside strings and variable ordering
// are normalized; values are not evaluated by the compiler or regenerated here.
func TestTLAPRFullConformance(t *testing.T) {
	jar := requireTLC(t)
	for _, name := range []string{"collections", "nondeterminism", "config_values", "qualified_config"} {
		t.Run(name, func(t *testing.T) {
			path := "testdata/module_conformance/" + name
			out := compileFixture(t, path+".tlapr")
			want := readModuleExpectedGraph(t, path+".json")
			run := runGeneratedTLC(t, jar, out, true)
			if run.exitCode != 0 {
				t.Fatalf("TLC exit = %d\n%s\n%s", run.exitCode, run.log, out.TLA)
			}
			got, err := parseModuleStateGraph(run.graph)
			if err != nil {
				t.Fatalf("TLC graph: %v\n%s", err, run.graph)
			}
			if err := graphDifference(got, want); err != nil {
				t.Fatal(err)
			}
			t.Logf("%d状態・%d初期状態・%d遷移が独立した期待値と一致", len(got.states), len(got.initial), len(got.transitions))
		})
	}
}

func TestTLAPRFullJunctionSelectors(t *testing.T) {
	jar := requireTLC(t)
	out := compileFixture(t, "testdata/module_conformance/junctions.tlapr")
	run := runGeneratedTLC(t, jar, out, false)
	if run.exitCode != 0 || !strings.Contains(run.log, "Model checking completed. No error has been found.") {
		t.Fatalf("junction selectors changed under whitespace: exit=%d\n%s", run.exitCode, run.log)
	}
}

func TestTLAPRFullFairnessAndLiveness(t *testing.T) {
	jar := requireTLC(t)
	for _, tc := range []struct {
		name, fixture, before, after string
		wantViolation                bool
	}{
		{name: "weak fairness ensures continuous progress", fixture: "weak_fairness"},
		{name: "missing fairness admits infinite stuttering", fixture: "weak_fairness", before: "WF_(done)(Finish)", after: "TRUE", wantViolation: true},
		{name: "strong fairness ensures intermittent progress", fixture: "strong_fairness"},
		{name: "weak fairness does not ensure intermittent progress", fixture: "strong_fairness", before: "SF_(vars)(Finish)", after: "WF_(vars)(Finish)", wantViolation: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := compileFixture(t, "testdata/module_conformance/"+tc.fixture+".tlapr")
			if tc.before != "" {
				out.TLA = replaceExactlyOnce(t, out.TLA, tc.before, tc.after)
			}
			run := runGeneratedTLC(t, jar, out, false)
			if tc.wantViolation {
				if run.exitCode != 13 || !strings.Contains(run.log, "Temporal properties were violated.") {
					t.Fatalf("liveness counterexample was not detected: exit=%d\n%s\n%s", run.exitCode, run.log, out.TLA)
				}
			} else if run.exitCode != 0 || !strings.Contains(run.log, "Model checking completed. No error has been found.") {
				t.Fatalf("TLC exit=%d\n%s\n%s", run.exitCode, run.log, out.TLA)
			}
		})
	}
}

// A wrong EXCEPT value can preserve all declared invariants. The independent
// collection-state oracle still has to reject it; the next-state values matter.
func TestTLAPRFullDetectsWrongCollectionUpdate(t *testing.T) {
	jar := requireTLC(t)
	const path = "testdata/module_conformance/collections"
	out := compileFixture(t, path+".tlapr")
	out.TLA = replaceExactlyOnce(t, out.TLA, "![4] = (@ + 2)", "![4] = (@ + 3)")
	run := runGeneratedTLC(t, jar, out, true)
	if run.exitCode != 0 {
		t.Fatalf("wrong-update control must still satisfy the DSL invariants: exit=%d\n%s", run.exitCode, run.log)
	}
	got, err := parseModuleStateGraph(run.graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := graphDifference(got, readModuleExpectedGraph(t, path+".json")); err == nil {
		t.Fatal("wrong function update escaped the independent oracle")
	}
}

func readModuleExpectedGraph(t *testing.T, path string) stateGraph {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		States      []map[string]string `json:"states"`
		Initial     []int               `json:"initial"`
		Transitions [][2]int            `json:"transitions"`
	}
	if err := json.Unmarshal(data, &expected, json.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
	if len(expected.States) == 0 || len(expected.Initial) == 0 {
		t.Fatal("module oracle must specify states and initial states")
	}
	var graph stateGraph
	for _, values := range expected.States {
		state, err := canonicalModuleState(values)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(graph.states, state) {
			t.Fatal("duplicate expected state")
		}
		graph.states = append(graph.states, state)
	}
	stateAt := func(index int) string {
		if index < 0 || index >= len(graph.states) {
			t.Fatalf("expected state index is out of range: %d", index)
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

func canonicalModuleState(values map[string]string) (string, error) {
	if len(values) == 0 {
		return "", fmt.Errorf("empty module state")
	}
	normalized := make(map[string]string, len(values))
	for name, value := range values {
		text, err := normalizeTLCValue(value)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		normalized[name] = text
	}
	data, err := json.Marshal(normalized, json.Deterministic(true))
	return string(data), err
}

func normalizeTLCValue(text string) (string, error) {
	var result strings.Builder
	quoted, escaped := false, false
	for _, ch := range text {
		if quoted {
			result.WriteRune(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = false
			}
		} else if ch == '"' {
			quoted = true
			result.WriteRune(ch)
		} else if !unicode.IsSpace(ch) {
			result.WriteRune(ch)
		}
	}
	if quoted || result.Len() == 0 {
		return "", fmt.Errorf("empty value or unterminated string: %q", text)
	}
	return result.String(), nil
}

func parseModuleStateGraph(data []byte) (stateGraph, error) {
	var graph stateGraph
	states := make(map[string]string)
	initial := make(map[string]bool)
	var edges [][2]string
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
			values := make(map[string]string)
			for assignment := range strings.SplitSeq(strings.TrimPrefix(label, "/\\ "), "\n/\\ ") {
				name, value, ok := strings.Cut(assignment, " = ")
				if !ok || name == "" || strings.ContainsAny(name, " \n\t") {
					return graph, fmt.Errorf("unsupported state assignment: %q", assignment)
				}
				if _, duplicate := values[name]; duplicate {
					return graph, fmt.Errorf("duplicate state variable: %s", name)
				}
				values[name] = value
			}
			state, err := canonicalModuleState(values)
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

func TestTLAPRFullGraphReader(t *testing.T) {
	const dot = `strict digraph DiskGraph {
nodesep=0.35;
subgraph cluster_graph {
color="white";
-9 [label="/\\ value = [text |-> \"two words\", items |-> {1, 2}]\n/\\ queue = <<1, 2>>",style = filled]
-9 -> 42 [label="Advance"];
42 [label="/\\ queue = <<2, 1>>\n/\\ value = [text |-> \"two words\", items |-> {2}]"];
{rank = same; -9;}
{rank = same; 42;}
}
}`
	got, err := parseModuleStateGraph([]byte(dot))
	if err != nil {
		t.Fatal(err)
	}
	start, err := canonicalModuleState(map[string]string{"queue": "<< 1, 2 >>", "value": `[text |-> "two words", items |-> {1, 2}]`})
	if err != nil {
		t.Fatal(err)
	}
	end, err := canonicalModuleState(map[string]string{"queue": "<< 2, 1 >>", "value": `[text |-> "two words", items |-> {2}]`})
	if err != nil {
		t.Fatal(err)
	}
	want := normalizeGraph(stateGraph{states: []string{start, end}, initial: []string{start}, transitions: []string{start + " -> " + end}})
	if err := graphDifference(got, want); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(start, "two words") {
		t.Fatal("whitespace inside quoted values must be preserved")
	}
	for _, broken := range []string{
		strings.TrimSuffix(dot, "\n}"),
		strings.ReplaceAll(dot, ",style = filled", ""),
		strings.Replace(dot, "-9 -> 42", "-9 -> 100", 1),
		strings.Replace(dot, "nodesep=0.35;", "unrecognized=true;", 1),
	} {
		if _, err := parseModuleStateGraph([]byte(broken)); err == nil {
			t.Fatal("incomplete or unsupported graph was accepted")
		}
	}
}
