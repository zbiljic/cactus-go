package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zbiljic/cactus-go"
)

func TestParseEvaluationCase(t *testing.T) {
	t.Parallel()

	evaluation, err := parseEvaluationCase([]byte(`{
		"input":"weather in Paris, then set a timer",
		"want":[
			"get_weather",
			{"name":"set_timer","arguments":{"minutes":10}}
		]
	}`))
	if err != nil {
		t.Fatalf("parseEvaluationCase() error = %v", err)
	}
	if evaluation.input != "weather in Paris, then set a timer" || len(evaluation.want) != 2 {
		t.Fatalf("evaluation = %#v", evaluation)
	}
	if evaluation.want[0].name != "get_weather" || evaluation.want[0].hasArguments {
		t.Fatalf("name-only expectation = %#v", evaluation.want[0])
	}
	if evaluation.want[1].name != "set_timer" || !evaluation.want[1].hasArguments ||
		!json.Valid(evaluation.want[1].arguments) {
		t.Fatalf("exact expectation = %#v", evaluation.want[1])
	}
}

func TestParseEvaluationCaseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "missing input", data: `{"want":[]}`, want: "input is required"},
		{name: "empty input", data: `{"input":" ","want":[]}`, want: "input is empty"},
		{name: "missing want", data: `{"input":"hello"}`, want: "want is required"},
		{name: "null want", data: `{"input":"hello","want":null}`, want: "want must be an array"},
		{name: "unknown case field", data: `{"input":"hello","want":[],"extra":true}`, want: `unknown field "extra"`},
		{name: "empty tool name", data: `{"input":"hello","want":[""]}`, want: "tool name is empty"},
		{name: "missing call name", data: `{"input":"hello","want":[{"arguments":{}}]}`, want: "name is required"},
		{name: "unknown call field", data: `{"input":"hello","want":[{"name":"one","extra":true}]}`, want: `unknown field "extra"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseEvaluationCase([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseEvaluationCase() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestEvaluateResponse(t *testing.T) {
	t.Parallel()

	response := cactus.Response{
		Type:    cactus.ResponseCall,
		Success: true,
		FunctionCalls: []cactus.FunctionCall{
			{Name: "get_weather", Arguments: json.RawMessage(`{"city":"Paris"}`)},
			{Name: "set_timer", Arguments: json.RawMessage(`{"minutes":10.0}`)},
		},
	}
	want := []evaluationExpectation{
		{name: "get_weather"},
		{name: "set_timer", arguments: json.RawMessage(`{"minutes":10}`), hasArguments: true},
	}
	result := evaluateResponse(response, want)
	if !result.call || !result.name || !result.exact || result.actual != "get_weather, set_timer" {
		t.Fatalf("evaluation result = %#v", result)
	}

	want[1].arguments = json.RawMessage(`{"minutes":5}`)
	result = evaluateResponse(response, want)
	if !result.name || result.exact || result.detail != " (argument mismatch)" {
		t.Fatalf("mismatched result = %#v", result)
	}

	result = evaluateResponse(cactus.Response{Type: cactus.ResponseRespond, Success: true}, nil)
	if result.call || !result.name || !result.exact || result.actual != "(respond)" {
		t.Fatalf("no-call result = %#v", result)
	}
}

func TestEval(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	toolsPath := filepath.Join(temporary, "tools.json")
	casesPath := filepath.Join(temporary, "cases.jsonl")
	writeTestFile(t, toolsPath, `[
		{"name":"get_weather","parameters":{"type":"object"}},
		{"name":"set_timer","parameters":{"type":"object"}}
	]`)
	writeTestFile(t, casesPath, strings.Join([]string{
		`{"input":"weather in Paris","want":["get_weather"]}`,
		`{"input":"timer for ten minutes","want":[{"name":"set_timer","arguments":{"minutes":10}}]}`,
	}, "\n"))

	confidence := 0.75
	fake := &fakeAgent{responses: []cactus.Response{
		{
			Type:       cactus.ResponseCall,
			Success:    true,
			Confidence: &confidence,
			FunctionCalls: []cactus.FunctionCall{{
				Name:      "get_weather",
				Arguments: json.RawMessage(`{"city":"Paris"}`),
			}},
		},
		{
			Type:       cactus.ResponseCall,
			Success:    true,
			Confidence: &confidence,
			FunctionCalls: []cactus.FunctionCall{{
				Name:      "set_timer",
				Arguments: json.RawMessage(`{"minutes":10.0}`),
			}},
		},
	}}
	app, stdout, stderr := testApplication("")
	var gotConfig cactus.Config
	app.deps.newAgent = func(_ context.Context, config cactus.Config) (session, error) {
		gotConfig = config
		return fake, nil
	}
	code := app.run(context.Background(), []string{
		"eval",
		"--tools", toolsPath,
		"--cases", casesPath,
		"--max-tokens", "64",
		"--min-score", "1",
	})
	if code != 0 {
		t.Fatalf("eval exit code = %d, stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if fake.resets != 2 || !reflect.DeepEqual(fake.inputs, []string{"weather in Paris", "timer for ten minutes"}) {
		t.Fatalf("eval resets = %d inputs = %#v", fake.resets, fake.inputs)
	}
	if len(gotConfig.Tools) != 2 || gotConfig.Tools[1].Schema.Name != "set_timer" {
		t.Fatalf("eval tools = %#v", gotConfig.Tools)
	}
	if !reflect.DeepEqual(fake.tokens, []int{64, 64}) {
		t.Fatalf("eval tokens = %#v", fake.tokens)
	}
	if !strings.Contains(stdout.String(), "exact 2/2, score 1.000") ||
		!strings.Contains(stdout.String(), "confidence 0.75") {
		t.Fatalf("eval output = %q", stdout.String())
	}
}

func TestEvalJSON(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	toolsPath := filepath.Join(temporary, "tools.json")
	casesPath := filepath.Join(temporary, "cases.jsonl")
	writeTestFile(t, toolsPath, `[
		{"name":"get_weather","parameters":{"type":"object"}},
		{"name":"set_timer","parameters":{"type":"object"}}
	]`)
	writeTestFile(t, casesPath, strings.Join([]string{
		`{"input":"weather in Paris","want":[{"name":"get_weather","arguments":{"city":"Paris"}}]}`,
		`{"input":"timer for ten minutes","want":["set_timer"]}`,
		`{"input":"capital of France","want":[]}`,
	}, "\n"))

	confidence := 0.75
	fake := &fakeAgent{responses: []cactus.Response{
		{
			Type:       cactus.ResponseCall,
			Success:    true,
			Confidence: &confidence,
			FunctionCalls: []cactus.FunctionCall{{
				Name:      "get_weather",
				Arguments: json.RawMessage(`{"city":"Paris"}`),
			}},
		},
		{
			Type:       cactus.ResponseCall,
			Success:    true,
			Confidence: &confidence,
			FunctionCalls: []cactus.FunctionCall{{
				Name:      "set_timer",
				Arguments: json.RawMessage(`{"minutes":10}`),
			}},
		},
		{Type: cactus.ResponseRespond, Success: true, Confidence: &confidence},
	}}
	app, stdout, stderr := testApplication("")
	app.deps.newAgent = func(context.Context, cactus.Config) (session, error) {
		return fake, nil
	}
	if code := app.run(context.Background(), []string{
		"eval",
		"--tools", toolsPath,
		"--cases", casesPath,
		"--format", "json",
		"--min-score", "1",
	}); code != 0 {
		t.Fatalf("JSON eval exit code = %d, stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}

	var report evaluationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode JSON report: %v; output = %q", err, stdout.String())
	}
	if report.SchemaVersion != evaluationReportSchemaVersion ||
		len(report.Cases) != 3 || report.Cases[0].Input != "weather in Paris" {
		t.Fatalf("JSON cases = %#v", report.Cases)
	}
	weather := report.Cases[0]
	if !weather.CallResponse || !weather.NameMatch || !weather.ExactMatch ||
		len(weather.Want) != 1 || string(weather.Want[0].Arguments) != `{"city":"Paris"}` ||
		len(weather.Response.FunctionCalls) != 1 || weather.Response.FunctionCalls[0].Name != "get_weather" {
		t.Fatalf("weather report = %#v", weather)
	}
	if report.Cases[1].Want[0].Arguments != nil {
		t.Fatalf("name-only expectation arguments = %s", report.Cases[1].Want[0].Arguments)
	}
	noCall := report.Cases[2]
	if noCall.CallResponse || !noCall.NameMatch || !noCall.ExactMatch || len(noCall.Want) != 0 {
		t.Fatalf("no-call report = %#v", noCall)
	}
	if report.Summary.Total != 3 || report.Summary.CallResponses != 2 ||
		report.Summary.NameMatches != 3 || report.Summary.ExactMatches != 3 ||
		report.Summary.Score != 1 || report.Summary.MinimumScore != 1 || !report.Summary.Passed ||
		report.Summary.AverageConfidence == nil || *report.Summary.AverageConfidence != confidence {
		t.Fatalf("JSON summary = %#v", report.Summary)
	}
}

func TestEvalMinimumScore(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	toolsPath := filepath.Join(temporary, "tools.json")
	casesPath := filepath.Join(temporary, "cases.jsonl")
	writeTestFile(t, toolsPath, `[{"name":"get_weather","parameters":{"type":"object"}}]`)
	writeTestFile(t, casesPath, `{"input":"weather in Paris","want":["get_weather"]}`)
	response := cactus.Response{
		Type:    cactus.ResponseCall,
		Success: true,
		FunctionCalls: []cactus.FunctionCall{{
			Name:      "set_timer",
			Arguments: json.RawMessage(`{"minutes":10}`),
		}},
	}

	app, stdout, stderr := testApplication("")
	app.deps.newAgent = func(context.Context, cactus.Config) (session, error) {
		return &fakeAgent{responses: []cactus.Response{response}}, nil
	}
	if code := app.run(context.Background(), []string{
		"eval", "--tools", toolsPath, "--cases", casesPath, "--min-score", "1",
	}); code != 1 {
		t.Fatalf("threshold eval exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "score 0.000") ||
		!strings.Contains(stderr.String(), "below --min-score") {
		t.Fatalf("threshold stdout = %q stderr = %q", stdout.String(), stderr.String())
	}

	app, _, stderr = testApplication("")
	app.deps.newAgent = func(context.Context, cactus.Config) (session, error) {
		return &fakeAgent{responses: []cactus.Response{response}}, nil
	}
	if code := app.run(context.Background(), []string{
		"eval", "--tools", toolsPath, "--cases", casesPath,
	}); code != 0 {
		t.Fatalf("non-gating eval exit code = %d, stderr = %q", code, stderr.String())
	}

	app, stdout, stderr = testApplication("")
	app.deps.newAgent = func(context.Context, cactus.Config) (session, error) {
		return &fakeAgent{responses: []cactus.Response{response}}, nil
	}
	if code := app.run(context.Background(), []string{
		"eval", "--tools", toolsPath, "--cases", casesPath, "--format", "json", "--min-score", "1",
	}); code != 1 {
		t.Fatalf("threshold JSON eval exit code = %d", code)
	}
	var report evaluationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode threshold JSON report: %v; output = %q", err, stdout.String())
	}
	if report.Summary.Passed || report.Summary.Score != 0 || report.Summary.MinimumScore != 1 ||
		!strings.Contains(stderr.String(), "below --min-score") {
		t.Fatalf("threshold JSON summary = %#v stderr = %q", report.Summary, stderr.String())
	}
}

func TestEvalUsageAndInvalidCases(t *testing.T) {
	t.Parallel()

	app, _, _ := testApplication("")
	if code := app.run(context.Background(), []string{"eval"}); code != 2 {
		t.Fatalf("missing eval flags exit code = %d", code)
	}
	if code := app.run(context.Background(), []string{
		"eval", "--tools", "tools.json", "--cases", "cases.jsonl", "--min-score", "2",
	}); code != 2 {
		t.Fatalf("invalid score exit code = %d", code)
	}
	if code := app.run(context.Background(), []string{
		"eval", "--tools", "tools.json", "--cases", "cases.jsonl", "--format", "yaml",
	}); code != 2 {
		t.Fatalf("invalid format exit code = %d", code)
	}

	temporary := t.TempDir()
	toolsPath := filepath.Join(temporary, "tools.json")
	casesPath := filepath.Join(temporary, "cases.jsonl")
	writeTestFile(t, toolsPath, `[{"name":"one","parameters":{"type":"object"}}]`)
	writeTestFile(t, casesPath, "{}\n")
	app, _, stderr := testApplication("")
	if code := app.run(context.Background(), []string{
		"eval", "--tools", toolsPath, "--cases", casesPath,
	}); code != 1 {
		t.Fatalf("invalid cases exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "line 1: input is required") {
		t.Fatalf("invalid cases stderr = %q", stderr.String())
	}
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateResponseMatchesInAnyOrder(t *testing.T) {
	t.Parallel()

	response := cactus.Response{Type: cactus.ResponseCall, Success: true, FunctionCalls: []cactus.FunctionCall{
		{Name: "set_timer", Arguments: json.RawMessage(`{"minutes":5}`)},
		{Name: "set_timer", Arguments: json.RawMessage(`{"minutes":10}`)},
		{Name: "get_weather", Arguments: json.RawMessage(`{"city":"Paris"}`)},
	}}
	tests := []struct {
		name                string
		want                []evaluationExpectation
		wantName, wantExact bool
	}{
		{"reordered", []evaluationExpectation{
			{name: "get_weather", arguments: json.RawMessage(`{"city":"Paris"}`), hasArguments: true},
			{name: "set_timer", arguments: json.RawMessage(`{"minutes":10}`), hasArguments: true},
			{name: "set_timer", arguments: json.RawMessage(`{"minutes":5}`), hasArguments: true},
		}, true, true},
		{"name-only listed before the call it must not take", []evaluationExpectation{
			{name: "set_timer"},
			{name: "set_timer", arguments: json.RawMessage(`{"minutes":5}`), hasArguments: true},
			{name: "get_weather"},
		}, true, true},
		{"argument mismatch", []evaluationExpectation{
			{name: "set_timer", arguments: json.RawMessage(`{"minutes":5}`), hasArguments: true},
			{name: "set_timer", arguments: json.RawMessage(`{"minutes":5}`), hasArguments: true},
			{name: "get_weather"},
		}, true, false},
		{"missing call", []evaluationExpectation{{name: "set_timer"}, {name: "get_weather"}}, false, false},
		{"string case differs", []evaluationExpectation{
			{name: "get_weather", arguments: json.RawMessage(`{"city":"paris"}`), hasArguments: true},
			{name: "set_timer"}, {name: "set_timer"},
		}, true, true},
		{"string differs", []evaluationExpectation{
			{name: "get_weather", arguments: json.RawMessage(`{"city":"Lyon"}`), hasArguments: true},
			{name: "set_timer"}, {name: "set_timer"},
		}, true, false},
	}
	for _, test := range tests {
		result := evaluateResponse(response, test.want)
		if result.name != test.wantName || result.exact != test.wantExact {
			t.Errorf("%s: name = %v exact = %v, want %v %v (%s)", test.name, result.name, result.exact, test.wantName, test.wantExact, result.detail)
		}
	}
}

func TestEvaluateResponseRejectsFlaggedCalls(t *testing.T) {
	t.Parallel()

	response := cactus.Response{
		Type: cactus.ResponseCall, Success: true,
		FunctionCalls: []cactus.FunctionCall{{Name: "pay", Arguments: json.RawMessage(`{"amount":25.5}`)}},
		Validation:    &cactus.Validation{Ungrounded: []string{"pay.amount"}},
	}
	want := []evaluationExpectation{{name: "pay", arguments: json.RawMessage(`{"amount":25.5}`), hasArguments: true}}
	if result := evaluateResponse(response, want, "pay $25.50"); !result.exact || result.rejected != "" {
		t.Fatalf("sourced number: %#v", result)
	}
	result := evaluateResponse(response, want, "pay the usual")
	if result.exact || result.name || !strings.Contains(result.rejected, "pay.amount") ||
		!strings.Contains(result.detail, "rejected:") {
		t.Fatalf("unsourced number: %#v", result)
	}
	if result := evaluateResponse(response, nil, "pay the usual"); !result.exact {
		t.Fatalf("rejected call scored against no call: %#v", result)
	}
}

func TestEvalCriticalCases(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	toolsPath := filepath.Join(temporary, "tools.json")
	casesPath := filepath.Join(temporary, "cases.jsonl")
	systemPath := filepath.Join(temporary, "system.txt")
	writeTestFile(t, toolsPath, `[{"name":"set_timer","parameters":{"type":"object"}}]`)
	writeTestFile(t, systemPath, "budget: 40\n")
	writeTestFile(t, casesPath, strings.Join([]string{
		`{"input":"timer for 75","want":[{"name":"set_timer","arguments":{"minutes":75}}]}`,
		`{"input":"a timer for negative 5 minutes","want":[],"critical":true}`,
		`{"input":"start a timer","want":[],"critical":false}`,
		`{"input":"timer for the budget","want":[{"name":"set_timer","arguments":{"minutes":40}}]}`,
	}, "\n"))
	call := func(arguments, validation string) cactus.Response {
		response := cactus.Response{Type: cactus.ResponseCall, Success: true,
			FunctionCalls: []cactus.FunctionCall{{Name: "set_timer", Arguments: json.RawMessage(arguments)}}}
		if validation != "" {
			response.Validation = &cactus.Validation{Ungrounded: []string{validation}}
		}
		return response
	}
	fake := &fakeAgent{responses: []cactus.Response{
		call(`{"minutes":75}`, "set_timer.minutes"),
		call(`{"minutes":5}`, ""),
		call(`{"minutes":1}`, "set_timer.minutes"),
		call(`{"minutes":40}`, "set_timer.minutes"),
	}}
	app, stdout, stderr := testApplication("")
	var gotConfig cactus.Config
	app.deps.newAgent = func(_ context.Context, config cactus.Config) (session, error) {
		gotConfig = config
		return fake, nil
	}
	code := app.run(context.Background(), []string{
		"eval", "--tools", toolsPath, "--cases", casesPath, "--system-file", systemPath, "--format", "json",
	})
	if code == 0 || !strings.Contains(stderr.String(), "critical cases failed: 1") {
		t.Fatalf("eval exit code = %d, stderr = %q", code, stderr.String())
	}
	if gotConfig.System != "budget: 40" {
		t.Fatalf("system = %q", gotConfig.System)
	}
	var report evaluationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	summary := report.Summary
	if report.SchemaVersion != 2 || summary.ExactMatches != 3 || summary.CriticalCases != 1 ||
		summary.CriticalFailures != 1 || summary.Passed {
		t.Fatalf("summary = %+v", summary)
	}
	if !report.Cases[1].Critical || report.Cases[1].ExactMatch || report.Cases[2].Rejected == "" || !report.Cases[2].ExactMatch {
		t.Fatalf("cases = %+v", report.Cases)
	}
}

func TestParseEvaluationCaseCritical(t *testing.T) {
	t.Parallel()

	evaluation, err := parseEvaluationCase([]byte(`{"input":"x","want":[],"critical":true}`))
	if err != nil || !evaluation.critical {
		t.Fatalf("parse = %+v, %v", evaluation, err)
	}
	if _, err := parseEvaluationCase([]byte(`{"input":"x","want":[],"critical":"yes"}`)); err == nil {
		t.Fatal("string critical accepted")
	}
}

func TestEvaluateResponseScoresFailedResponses(t *testing.T) {
	t.Parallel()

	code := "truncated"
	failed := cactus.Response{Type: cactus.ResponseCall, Success: false, ErrorCode: &code}
	if result := evaluateResponse(failed, nil); !result.exact || result.detail != " (response failed: truncated)" {
		t.Fatalf("failed refusal = %#v", result)
	}
	want := []evaluationExpectation{{name: "set_timer"}}
	if result := evaluateResponse(failed, want); result.exact || result.detail != " (want set_timer) (response failed: truncated)" {
		t.Fatalf("failed call = %#v", result)
	}
}

func TestEvalTextMarksCriticalFailuresAndRejections(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	toolsPath := filepath.Join(temporary, "tools.json")
	casesPath := filepath.Join(temporary, "cases.jsonl")
	writeTestFile(t, toolsPath, `[{"name":"skip_track","parameters":{"type":"object"}}]`)
	writeTestFile(t, casesPath, strings.Join([]string{
		`{"input":"don't skip this song","want":[],"critical":true}`,
		`{"input":"skip it","want":[],"critical":true}`,
		`{"input":"never skip","want":[]}`,
	}, "\n"))
	code := "truncated"
	fake := &fakeAgent{responses: []cactus.Response{
		{Type: cactus.ResponseCall, Success: true, FunctionCalls: []cactus.FunctionCall{{Name: "skip_track", Arguments: json.RawMessage(`{}`)}},
			Validation: &cactus.Validation{Negation: true}},
		{Type: cactus.ResponseCall, Success: true, FunctionCalls: []cactus.FunctionCall{{Name: "skip_track", Arguments: json.RawMessage(`{}`)}}},
		{Type: cactus.ResponseCall, Success: false, ErrorCode: &code},
	}}
	app, stdout, _ := testApplication("")
	app.deps.newAgent = func(context.Context, cactus.Config) (session, error) { return fake, nil }
	if exit := app.run(context.Background(), []string{"eval", "--tools", toolsPath, "--cases", casesPath}); exit == 0 {
		t.Fatal("eval passed with a critical failure")
	}
	output := stdout.String()
	for _, want := range []string{
		`✓ 1 "don't skip this song" -> skip_track (conf -, `,
		`) (rejected: cactus: response validation: negation detected)`,
		`✗! 2 "skip it" -> skip_track`,
		`✓ 3 "never skip" -> (no call)`,
		`(response failed: truncated)`,
		`critical failures 1/2, failed responses 1`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}
