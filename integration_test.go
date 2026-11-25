package cactus

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestNativeEmbeddings(t *testing.T) {
	if os.Getenv("CACTUS_TEST_NATIVE") != "1" {
		t.Skip("set CACTUS_TEST_NATIVE=1 to test native embeddings")
	}
	ctx := context.Background()
	type weatherArguments struct {
		City string `json:"city"`
		Day  string `json:"day,omitempty" jsonschema:"enum=today,enum=tomorrow,enum=weekend"`
	}
	a, err := New(ctx, Config{Tools: []Tool{{Schema: SchemaFor[weatherArguments]("get_weather", "Current or forecast weather for a city.")}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Embed(ctx, "turn on the kitchen lights")
	if err != nil || len(first) == 0 {
		t.Fatalf("Embed() length=%d err=%v", len(first), err)
	}
	second, err := a.Embed(ctx, "turn on the kitchen lights")
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("embedding is not repeatable: %v", err)
	}
	for _, value := range first {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			t.Fatalf("non-finite embedding value %v", value)
		}
	}
	// Compare a manual tool loop with and without an embedding between turns.
	var baseline Response
	for _, insertEmbedding := range []bool{false, true} {
		if err := a.Reset(ctx); err != nil {
			t.Fatal(err)
		}
		initial, err := a.Complete(ctx, "what's the weather in Paris tomorrow", DefaultMaxNewTokens)
		if err != nil || initial.Type != ResponseCall || len(initial.FunctionCalls) != 1 || initial.FunctionCalls[0].Name != "get_weather" {
			t.Fatalf("initial tool call: response=%+v err=%v", initial, err)
		}
		if insertEmbedding {
			if _, err := a.Embed(ctx, "Find Ada Lovelace in my contacts."); err != nil {
				t.Fatal(err)
			}
		}
		final, err := a.Complete(ctx, `[{"city":"Paris","tomorrow":"rain, 14C"}]`, DefaultMaxNewTokens)
		if err != nil || final.Type != ResponseRespond || !final.Success {
			t.Fatalf("tool-result continuation: response=%+v err=%v", final, err)
		}
		if insertEmbedding && (final.Type != baseline.Type || final.Reasoning != baseline.Reasoning || !reflect.DeepEqual(final.FunctionCalls, baseline.FunctionCalls) || !reflect.DeepEqual(final.Confidence, baseline.Confidence)) {
			t.Fatalf("embedding changed continuation: got=%+v baseline=%+v", final, baseline)
		}
		baseline = final
	}
}

func TestNativeEngine(t *testing.T) {
	libraryPath := os.Getenv("CACTUS_TEST_LIBRARY")
	if libraryPath == "" {
		t.Skip("CACTUS_TEST_LIBRARY is not set")
	}
	agent, err := New(context.Background(), Config{LibraryPath: libraryPath})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := agent.Complete(context.Background(), "hello", 32)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Type == "" {
		t.Fatal("Complete() returned an empty response type")
	}
}

func TestNativeGenerations(t *testing.T) {
	if os.Getenv("CACTUS_TEST_NATIVE") != "1" {
		t.Skip("set CACTUS_TEST_NATIVE=1 to test both generations in one process")
	}
	ctx := context.Background()
	var agents []*Agent
	for _, generation := range []int{2, 3} {
		a, err := New(ctx, Config{Generation: generation})
		if err != nil {
			t.Fatalf("generation %d: %v", generation, err)
		}
		agents = append(agents, a)
		api := runtimes[generation].api
		if (api.completeAudio != nil) != (generation == 3) || (generation == 3 && api.lastError == nil) {
			t.Fatalf("generation %d: unexpected native ABI", generation)
		}
	}
	// Alternate generations after both libraries have been loaded.
	for range 2 {
		for i, a := range agents {
			if err := a.Reset(ctx); err != nil {
				t.Fatal(err)
			}
			r, err := a.Complete(ctx, "hello", DefaultMaxNewTokens)
			if err != nil || !r.Success || r.Type != ResponseCall || len(r.FunctionCalls) != 0 {
				t.Fatalf("generation %d: response=%+v err=%v", i+2, r, err)
			}
			if i == 1 && r.Confidence == nil {
				t.Fatal("v3 base model lost confidence")
			}
		}
	}
	// Reuse the published archive as a custom model to exercise real reloads
	// without needing a separately fine-tuned fixture.
	blob, err := os.ReadFile(runtimes[3].activeWeights)
	if err != nil {
		t.Fatal(err)
	}
	customPath := filepath.Join(t.TempDir(), "custom.cact")
	if err := os.WriteFile(customPath, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	custom, err := New(ctx, Config{Generation: 2, WeightsPath: customPath})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []*Agent{custom, agents[1]} {
		r, err := a.Complete(ctx, "hello", DefaultMaxNewTokens)
		if err != nil || !r.Success || r.Confidence == nil {
			t.Fatalf("custom=%v response=%+v err=%v", a == custom, r, err)
		}
	}
}

func TestNativeEngineDiagnostics(t *testing.T) {
	if os.Getenv("CACTUS_TEST_NATIVE") != "1" {
		t.Skip("set CACTUS_TEST_NATIVE=1 to test native diagnostics")
	}
	_, err := New(context.Background(), Config{System: strings.Repeat("long context ", 10000)})
	if err == nil {
		t.Fatal("oversized system prompt was accepted")
	}
	_, detail, ok := strings.Cut(err.Error(), "initialize failed with code ")
	if !ok || !strings.Contains(detail, ": ") {
		t.Fatalf("native initialization diagnostic = %v", err)
	}
}

func TestNativeThreads(t *testing.T) {
	if os.Getenv("CACTUS_TEST_NATIVE") != "1" {
		t.Skip("set CACTUS_TEST_NATIVE=1 to test native thread control")
	}
	// The engine reads the count when it loads weights, so set it first.
	ctx := context.Background()
	if err := SetThreads(1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetThreads(0) })
	a, err := New(ctx, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	response, err := a.Complete(ctx, "hello", 32)
	if err != nil || !response.Success {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

// TestNativeDateGrounding checks date grounding on engine output: a date is
// flagged exactly when its year is neither written nor licensed.
func TestNativeDateGrounding(t *testing.T) {
	if os.Getenv("CACTUS_TEST_NATIVE") != "1" {
		t.Skip("set CACTUS_TEST_NATIVE=1 to test date grounding on the engine")
	}
	type event struct {
		Title string `json:"title"`
		Date  string `json:"date" jsonschema:"format=date"`
	}
	ctx := context.Background()
	agent, err := New(ctx, Config{
		Tools:     []Tool{{Schema: SchemaFor[event]("create_event", "Create a calendar event.")}},
		System:    "date: 2026-11-25 Wed 14:30",
		Stateless: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	checked := 0
	for _, test := range []struct {
		query string
		// years are the years a date may carry; nil means it is not checked.
		years []int
	}{
		{"plan the 2030 kickoff on Jan 3rd 2030", []int{2030}},
		{"add review on 5th September 2031", []int{2031}},
		{"add the March 2019 reunion follow-up tomorrow", []int{2019, 2026}},
		{"schedule dentist tomorrow", nil},
	} {
		response, err := agent.Complete(ctx, test.query, 0)
		if err != nil {
			t.Fatalf("Complete(%q) error = %v", test.query, err)
		}
		if len(response.FunctionCalls) != 1 {
			t.Logf("Complete(%q) returned %d calls; not checked", test.query, len(response.FunctionCalls))
			continue
		}
		var arguments event
		if err := json.Unmarshal(response.FunctionCalls[0].Arguments, &arguments); err != nil || len(arguments.Date) < 4 {
			t.Fatalf("Complete(%q) arguments = %s", test.query, response.FunctionCalls[0].Arguments)
		}
		year, _ := strconv.Atoi(arguments.Date[:4])
		wantFlag := test.years != nil && !slices.Contains(test.years, year)
		flagged := response.Validation != nil && slices.Contains(response.Validation.Ungrounded, "create_event.date")
		if flagged != wantFlag {
			t.Errorf("Complete(%q) date %s flagged = %v, want %v", test.query, arguments.Date, flagged, wantFlag)
		}
		t.Logf("%q -> %s flagged=%v", test.query, arguments.Date, flagged)
		checked++
	}
	if checked == 0 {
		t.Fatal("no case returned a single call to check")
	}
}
