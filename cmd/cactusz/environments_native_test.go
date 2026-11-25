package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zbiljic/cactus-go"
)

const (
	environmentsDir = "testdata/environments"
	// environmentThreads pins the engine's thread count, which changes the
	// model's output; the baseline holds only at this count.
	environmentThreads = 4
)

// TestNativeEnvironmentSuites runs each environment suite on the real engine
// and fails when the cases that fail differ from the baseline, in either
// direction, so the baseline always describes the pinned engine.
func TestNativeEnvironmentSuites(t *testing.T) {
	if os.Getenv("CACTUS_TEST_NATIVE") != "1" {
		t.Skip("set CACTUS_TEST_NATIVE=1 to run the environment suites on the engine")
	}
	if err := cactus.SetThreads(environmentThreads); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cactus.SetThreads(0) })
	data, err := os.ReadFile(filepath.Join(environmentsDir, "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	// baselines maps each suite to the 1-based indexes of its failing cases.
	var baselines map[string][]int
	if err := json.Unmarshal(data, &baselines); err != nil {
		t.Fatal(err)
	}
	suites, err := filepath.Glob(filepath.Join(environmentsDir, "*", "cases.jsonl"))
	if err != nil || len(suites) != len(baselines) {
		t.Fatalf("suites = %v, baselines = %d, error = %v", suites, len(baselines), err)
	}
	for _, casesPath := range suites {
		directory := filepath.Dir(casesPath)
		name := filepath.Base(directory)
		t.Run(name, func(t *testing.T) {
			baseline, ok := baselines[name]
			if !ok {
				t.Fatalf("no baseline for %s", name)
			}
			report := runEnvironmentSuite(t, directory)
			expected := make(map[int]bool, len(baseline))
			for _, index := range baseline {
				if index < 1 || index > len(report.Cases) || expected[index] {
					t.Fatalf("baseline index %d is out of range or repeated", index)
				}
				expected[index] = true
			}
			var failing []int
			for _, result := range report.Cases {
				if result.ExactMatch {
					if expected[result.Index] {
						t.Errorf("case %d %q now passes; remove it from the baseline", result.Index, result.Input)
					}
					continue
				}
				failing = append(failing, result.Index)
				message := fmt.Sprintf("case %d %q critical=%v -> %s%s", result.Index, result.Input,
					result.Critical, result.textResult.actual, result.textResult.detail)
				if expected[result.Index] {
					t.Log(message)
				} else {
					t.Error("new failure: " + message)
				}
			}
			summary := report.Summary
			t.Logf("exact %d/%d, critical failures %d, failing %v",
				summary.ExactMatches, summary.Total, summary.CriticalFailures, failing)
		})
	}
}

func runEnvironmentSuite(t *testing.T, directory string) evaluationReport {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schemas []cactus.ToolSchema
	if err := json.Unmarshal(data, &schemas); err != nil {
		t.Fatal(err)
	}
	system, err := os.ReadFile(filepath.Join(directory, "system.txt"))
	if err != nil {
		t.Fatal(err)
	}
	config := cactus.Config{System: strings.TrimRight(string(system), "\r\n")}
	for _, schema := range schemas {
		config.Tools = append(config.Tools, cactus.Tool{Schema: schema})
	}
	cases, err := readEvaluationCases(filepath.Join(directory, "cases.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	agent, err := cactus.New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = agent.Close() })
	report, err := evaluateCases(ctx, agent, cases, config.System, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	return report
}
