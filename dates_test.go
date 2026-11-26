package cactus

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestSourceYears(t *testing.T) {
	t.Parallel()

	tests := map[string][]int{
		"due on 5th September 2031":    {2031},
		"due on 5/6/24":                {},
		"due on 2024-05-06":            {2024},
		"due on 2024/5/6":              {2024},
		"due on 5 June 19:30":          {},
		"due on June 5 7 pm":           {},
		"due on June 5, 2031 19:30":    {2031},
		"due on 5 June 2031":           {2031},
		"due Dec. 25, 2026":            {2026},
		"due 25 Dec. 2026":             {2026},
		"due Sept. 12, 2025":           {2025},
		"due 12 Sept. 2025":            {2025},
		"due Sep. 2025":                {2025},
		"in the year 1999":             {1999},
		"order 20240-05-06":            {},
		"since March 2019, 2031-09-05": {2019, 2031},
		"5 June 2031pm":                {},
		"5 June 2031 pmx":              {2031},
		"due ٥ June ٢٠٣١":              {2031},
		"due 5 June 2031":              {2031},
		"é5 June 2031":                 {2031},
		"5 June 203٤x":                 {203},
		"2024-05-6٧8":                  {2024},
		"APRİL 1274":                   {},
		"x5 June 31":                   {},
		"5 June 31":                    {31},
		"goodyear 2031":                {},
		"xjune 2031":                   {},
		"xjune 5 31":                   {},
		"june 5 31":                    {31},
	}
	for text, want := range tests {
		var got []int
		for year := range sourceYears(text) {
			got = append(got, year)
		}
		slices.Sort(got)
		if len(got) != len(want) || len(want) > 0 && !slices.Equal(got, want) {
			t.Errorf("sourceYears(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestHasRelativeCue(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]bool{
		"invoice Acme tomorrow":  true,
		"due next Friday":        true,
		"due in 3 weeks":         true,
		"due on Monday":          true,
		"NOW":                    true,
		"nowhere and nothing":    false,
		"due in three weeks":     false,
		"due 5 June 2031":        false,
		"éthis":                  false,
		"this_":                  false,
		"due Fridays":            false,
		"since today":            true,
		"in ٣ days":              true,
		"a yesterday-ish answer": true,
		"THİS":                   true,
		"Thıs":                   true,
		"FRİDAY":                 true,
	} {
		if got := hasRelativeCue(text); got != want {
			t.Errorf("hasRelativeCue(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestDigitValue(t *testing.T) {
	t.Parallel()

	for digits, want := range map[string]int{"2031": 2031, "٢٠٣١": 2031, "２０２６": 2026, "\U0001d7d0\U0001d7ce": 20, "\U0001d7dc\U0001d7dd": 45} {
		if got := digitValue(digits); got != want {
			t.Errorf("digitValue(%q) = %d, want %d", digits, got, want)
		}
	}
}

var invoiceSchema = ToolSchema{Name: "Invoice", Parameters: json.RawMessage(
	`{"type":"object","properties":{"vendor":{"type":"string"},"due_date":{"type":"string","format":"date"}}}`,
)}

func invoiceCall(due string) []byte {
	return []byte(`{"type":"call","function_calls":[{"name":"Invoice","arguments":{"vendor":"Acme","due_date":"` + due + `"}}]}`)
}

func ungrounded(response Response) []string {
	if response.Validation == nil {
		return nil
	}
	return response.Validation.Ungrounded
}

func TestCompleteGroundsDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		system string
		input  string
		due    string
		want   []string
	}{
		{name: "contradicting year", input: "Send an invoice to Acme due on 5th September 2031", due: "2026-09-05", want: []string{"Invoice.due_date"}},
		{name: "written year", input: "Send an invoice to Acme due on 5th September 2031", due: "2031-09-05"},
		{name: "no written year", input: "Send Acme an invoice due next Friday", due: "2026-09-05"},
		{name: "slash date", input: "Send an invoice to Acme due on 5/6/24", due: "2024-05-06"},
		{name: "relative date licensed by date fact", system: "date: 2026-07-21 Tue 14:30", input: "invoice Acme tomorrow for the March 2019 reunion", due: "2026-07-22"},
		{name: "date fact without relative wording", system: "date: 2026-07-21 Tue 14:30", input: "invoice Acme for the March 2019 reunion", due: "2026-07-22", want: []string{"Invoice.due_date"}},
		{name: "relative wording without date fact", input: "invoice Acme tomorrow for the March 2019 reunion", due: "2026-07-22", want: []string{"Invoice.due_date"}},
		{name: "not a date string", input: "due on 5th September 2031", due: "soon"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeNative{responses: [][]byte{invoiceCall(test.due)}}
			agent, _ := newTestAgent(t, fake, Config{System: test.system, Tools: []Tool{{Schema: invoiceSchema}}})
			response, err := agent.Complete(context.Background(), test.input, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := ungrounded(response); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ungrounded = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCompleteKeepsEngineFlagsAndAddsDates(t *testing.T) {
	t.Parallel()

	fake := &fakeNative{responses: [][]byte{
		[]byte(`{"type":"call","function_calls":[{"name":"Invoice","arguments":{"due_date":"2026-09-05"}},{"name":"Invoice","arguments":{"due_date":"2025-01-01"}}],"validation":{"ungrounded":["Invoice.vendor","Invoice.due_date"]}}`),
	}}
	agent, _ := newTestAgent(t, fake, Config{Tools: []Tool{{Schema: invoiceSchema}}})
	response, err := agent.Complete(context.Background(), "bill Acme on 5th September 2031", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ungrounded(response), []string{"Invoice.vendor", "Invoice.due_date"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ungrounded = %v, want %v", got, want)
	}
}

func TestDateYearsFollowTheConversation(t *testing.T) {
	t.Parallel()

	t.Run("carry across turns until reset", func(t *testing.T) {
		t.Parallel()
		fake := &fakeNative{responses: [][]byte{invoiceCall("2031-09-05"), invoiceCall("2031-09-05"), invoiceCall("2031-09-05")}}
		agent, _ := newTestAgent(t, fake, Config{Tools: []Tool{{Schema: invoiceSchema}}})
		ctx := context.Background()
		for _, input := range []string{"bill Acme on 5th September 2031", `{"id":42,"since":"2019-03-01"}`} {
			if response, err := agent.Complete(ctx, input, 0); err != nil || ungrounded(response) != nil {
				t.Fatalf("Complete(%q) = %v, %v", input, ungrounded(response), err)
			}
		}
		if err := agent.Reset(ctx); err != nil {
			t.Fatal(err)
		}
		response, err := agent.Complete(ctx, "customer since March 2019, bill them", 0)
		if err != nil || !reflect.DeepEqual(ungrounded(response), []string{"Invoice.due_date"}) {
			t.Fatalf("after Reset = %v, %v", ungrounded(response), err)
		}
	})

	t.Run("stateless", func(t *testing.T) {
		t.Parallel()
		fake := &fakeNative{responses: [][]byte{invoiceCall("2031-09-05"), invoiceCall("2031-09-05")}}
		agent, _ := newTestAgent(t, fake, Config{Stateless: true, Tools: []Tool{{Schema: invoiceSchema}}})
		ctx := context.Background()
		if response, err := agent.Complete(ctx, "bill Acme on 5th September 2031", 0); err != nil || ungrounded(response) != nil {
			t.Fatalf("first = %v, %v", ungrounded(response), err)
		}
		response, err := agent.Complete(ctx, "customer since March 2019, bill them", 0)
		if err != nil || !reflect.DeepEqual(ungrounded(response), []string{"Invoice.due_date"}) {
			t.Fatalf("second = %v, %v", ungrounded(response), err)
		}
	})

	t.Run("another agent reinitializes the conversation", func(t *testing.T) {
		t.Parallel()
		fake := &fakeNative{responses: [][]byte{invoiceCall("2031-09-05"), []byte(`{"type":"respond"}`), invoiceCall("2031-09-05")}}
		first, runtime := newTestAgent(t, fake, Config{Tools: []Tool{{Schema: invoiceSchema}}})
		second, err := prepareAgent(Config{})
		if err != nil {
			t.Fatal(err)
		}
		second.runtime = runtime
		ctx := context.Background()
		if _, err := first.Complete(ctx, "bill Acme on 5th September 2031", 0); err != nil {
			t.Fatal(err)
		}
		if _, err := second.Complete(ctx, "hello", 0); err != nil {
			t.Fatal(err)
		}
		response, err := first.Complete(ctx, "customer since March 2019, bill them", 0)
		if err != nil || !reflect.DeepEqual(ungrounded(response), []string{"Invoice.due_date"}) {
			t.Fatalf("after rebind = %v, %v", ungrounded(response), err)
		}
	})
}

func TestUnlicensedDatesWalksTheSchema(t *testing.T) {
	t.Parallel()

	root := map[string]any{
		"type": "object",
		"$defs": map[string]any{
			"Invoice": map[string]any{"type": "object", "properties": map[string]any{
				"due_date": map[string]any{"type": "string", "format": "date"},
			}},
			"a/b~c": map[string]any{"type": "string", "format": "date"},
		},
		"properties": map[string]any{
			"shipment": map[string]any{"type": "object", "properties": map[string]any{
				"invoice": map[string]any{"$ref": "#/$defs/Invoice"},
			}},
			"maybe":   map[string]any{"anyOf": []any{map[string]any{"type": "null"}, map[string]any{"type": "string", "format": "date-time"}}},
			"either":  map[string]any{"anyOf": []any{map[string]any{"type": "string", "format": "date"}, map[string]any{"type": "integer"}}},
			"dates":   map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "date"}},
			"loop":    map[string]any{"$ref": "#/properties/loop"},
			"plain":   map[string]any{"type": "string"},
			"one":     map[string]any{"oneOf": []any{map[string]any{"type": "null"}, map[string]any{"type": "string", "format": "date"}}},
			"escaped": map[string]any{"$ref": "#/$defs/a~1b~0c"},
			"bare":    map[string]any{"type": "array"},
			"nested":  map[string]any{"type": "array", "format": "date", "items": map[string]any{"type": "string", "format": "date"}},
			"short":   map[string]any{"type": "string", "format": "date"},
		},
	}
	var arguments any
	if err := json.Unmarshal([]byte(`{
		"shipment": {"invoice": {"due_date": "2026-09-05"}},
		"maybe": "2026-09-05T10:00:00Z",
		"either": "2026-09-05",
		"dates": ["2031-01-01", "2026-01-01"],
		"loop": "2026-01-01",
		"plain": "2026-01-01",
		"short": "26-01-01",
		"one": "2026-01-01",
		"escaped": "2026-01-01",
		"bare": ["2026-01-01"],
		"nested": ["2026-01-01"],
		"undeclared": "2026-01-01"
	}`), &arguments); err != nil {
		t.Fatal(err)
	}
	got := unlicensedDates(root, arguments, map[int]bool{2031: true})
	want := []string{"dates[1]", "escaped", "maybe", "nested[0]", "one", "shipment.invoice.due_date"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unlicensedDates() = %v, want %v", got, want)
	}
}

func TestRunRejectsUngroundedDates(t *testing.T) {
	t.Parallel()

	fake := &fakeNative{responses: [][]byte{invoiceCall("2026-09-05")}}
	executed := 0
	agent, _ := newTestAgent(t, fake, Config{Tools: []Tool{{
		Schema: invoiceSchema,
		Handler: func(context.Context, json.RawMessage) (any, error) {
			executed++
			return "ok", nil
		},
	}}})
	_, err := agent.Run(context.Background(), "Send an invoice to Acme due on 5th September 2031", 0, 0)
	if !errors.Is(err, ErrValidation) || executed != 0 {
		t.Fatalf("Run() error = %v, executed = %d", err, executed)
	}
}

func TestRunDoesNotGroundLaterRounds(t *testing.T) {
	t.Parallel()

	fake := &fakeNative{responses: [][]byte{
		invoiceCall("2031-09-05"),
		[]byte(`{"type":"call","function_calls":[{"name":"Remind","arguments":{"on":"2026-09-01"}}]}`),
		[]byte(`{"type":"respond"}`),
	}}
	remind := ToolSchema{Name: "Remind", Parameters: json.RawMessage(`{"type":"object","properties":{"on":{"type":"string","format":"date"}}}`)}
	ok := func(context.Context, json.RawMessage) (any, error) { return "ok", nil }
	agent, _ := newTestAgent(t, fake, Config{Tools: []Tool{{Schema: invoiceSchema, Handler: ok}, {Schema: remind, Handler: ok}}})
	response, err := agent.Run(context.Background(), "bill Acme on 5th September 2031", 0, 0)
	if err != nil || len(response.Results) != 2 {
		t.Fatalf("Run() results = %v, error = %v", response.Results, err)
	}
}

func TestWhistleCompleteGroundsDatesInHeardText(t *testing.T) {
	t.Parallel()

	fake := &fakeNative{responses: [][]byte{
		[]byte(`{"type":"call","function_calls":[{"name":"Invoice","arguments":{"due_date":"2026-09-05"}}],"audio_text":"bill Acme on 5th September 2031"}`),
		[]byte(`{"type":"call","function_calls":[{"name":"Invoice","arguments":{"due_date":"2031-09-05"}}],"audio_text":"and again"}`),
	}}
	agent, runtime := newTestAgent(t, fake, Config{Tools: []Tool{{Schema: invoiceSchema}}})
	speech := newTestWhistle(t, runtime)
	ctx := context.Background()
	response, err := speech.Complete(ctx, agent, nil, AudioOptions{}, 0)
	if err != nil || !reflect.DeepEqual(ungrounded(response), []string{"Invoice.due_date"}) {
		t.Fatalf("first = %v, %v", ungrounded(response), err)
	}
	response, err = speech.Complete(ctx, agent, nil, AudioOptions{}, 0)
	if err != nil || ungrounded(response) != nil {
		t.Fatalf("second = %v, %v", ungrounded(response), err)
	}
}

func TestWhistleCompleteForgetsYearsWhenStateless(t *testing.T) {
	t.Parallel()

	fake := &fakeNative{responses: [][]byte{
		[]byte(`{"type":"call","function_calls":[{"name":"Invoice","arguments":{"due_date":"2031-09-05"}}],"audio_text":"bill Acme on 5th September 2031"}`),
		[]byte(`{"type":"call","function_calls":[{"name":"Invoice","arguments":{"due_date":"2031-09-05"}}],"audio_text":"customer since March 2019, bill them"}`),
	}}
	agent, runtime := newTestAgent(t, fake, Config{Stateless: true, Tools: []Tool{{Schema: invoiceSchema}}})
	speech := newTestWhistle(t, runtime)
	ctx := context.Background()
	if response, err := speech.Complete(ctx, agent, nil, AudioOptions{}, 0); err != nil || ungrounded(response) != nil {
		t.Fatalf("first = %v, %v", ungrounded(response), err)
	}
	response, err := speech.Complete(ctx, agent, nil, AudioOptions{}, 0)
	if err != nil || !reflect.DeepEqual(ungrounded(response), []string{"Invoice.due_date"}) {
		t.Fatalf("second = %v, %v", ungrounded(response), err)
	}
}

func TestWhistleCompleteLicensesRelativeHeardDates(t *testing.T) {
	t.Parallel()

	fake := &fakeNative{responses: [][]byte{
		[]byte(`{"type":"call","function_calls":[{"name":"Invoice","arguments":{"due_date":"2026-07-22"}}],"audio_text":"invoice Acme tomorrow for the March 2019 order"}`),
	}}
	agent, runtime := newTestAgent(t, fake, Config{System: "date: 2026-07-21 Tue 14:30", Tools: []Tool{{Schema: invoiceSchema}}})
	speech := newTestWhistle(t, runtime)
	response, err := speech.Complete(context.Background(), agent, nil, AudioOptions{}, 0)
	if err != nil || ungrounded(response) != nil {
		t.Fatalf("relative heard date = %v, %v", ungrounded(response), err)
	}
}
