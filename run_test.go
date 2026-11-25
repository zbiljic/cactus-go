package cactus

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestRunExecutesEachToolInOneTurn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		responses    []string
		wantExecuted []string
		wantType     ResponseType
	}{
		{
			name: "repeated call",
			responses: []string{
				`{"type":"call","function_calls":[{"name":"event","arguments":{"title":"Dentist"}}]}`,
				`{"type":"call","function_calls":[{"name":"event","arguments":{"title":"Dentist"}}]}`,
			},
			wantExecuted: []string{`event{"title":"Dentist"}`},
			wantType:     ResponseCall,
		},
		{
			name: "same tool with altered arguments",
			responses: []string{
				`{"type":"call","function_calls":[{"name":"event","arguments":{"date":"2027-03-04"}}]}`,
				`{"type":"call","function_calls":[{"name":"event","arguments":{"date":"2026-03-04"}}]}`,
			},
			wantExecuted: []string{`event{"date":"2027-03-04"}`},
			wantType:     ResponseCall,
		},
		{
			name: "used tool beside a new one",
			responses: []string{
				`{"type":"call","function_calls":[{"name":"event","arguments":{"title":"A"}}]}`,
				`{"type":"call","function_calls":[{"name":"other","arguments":{"title":"B"}},{"name":"event","arguments":{"title":"C"}}]}`,
			},
			wantExecuted: []string{`event{"title":"A"}`},
			wantType:     ResponseCall,
		},
		{
			name: "tool used twice in one turn",
			responses: []string{
				`{"type":"call","function_calls":[{"name":"event","arguments":{"title":"A"}},{"name":"event","arguments":{"title":"B"}}]}`,
				`{"type":"respond"}`,
			},
			wantExecuted: []string{`event{"title":"A"}`, `event{"title":"B"}`},
			wantType:     ResponseRespond,
		},
		{
			name: "failed call counts as executed",
			responses: []string{
				`{"type":"call","function_calls":[{"name":"fail","arguments":{"city":"Paris"}}]}`,
				`{"type":"call","function_calls":[{"name":"fail","arguments":{"city":"Paris"}}]}`,
			},
			wantExecuted: []string{`fail{"city":"Paris"}`},
			wantType:     ResponseCall,
		},
		{
			name: "another tool in a later turn",
			responses: []string{
				`{"type":"call","function_calls":[{"name":"event","arguments":{"title":"A"}}]}`,
				`{"type":"call","function_calls":[{"name":"other","arguments":{"title":"A"}}]}`,
				`{"type":"respond"}`,
			},
			wantExecuted: []string{`event{"title":"A"}`, `other{"title":"A"}`},
			wantType:     ResponseRespond,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeNative{}
			for _, response := range test.responses {
				fake.responses = append(fake.responses, []byte(response))
			}
			var executed []string
			tools := make([]Tool, 0, 2)
			for _, name := range []string{"event", "other", "fail"} {
				tools = append(tools, Tool{
					Schema: ToolSchema{Name: name},
					Handler: func(_ context.Context, arguments json.RawMessage) (any, error) {
						executed = append(executed, name+string(arguments))
						if name == "fail" {
							return nil, errors.New("unavailable")
						}
						return "ok", nil
					},
				})
			}
			agent, _ := newTestAgent(t, fake, Config{Tools: tools})
			response, err := agent.Run(context.Background(), "add it", 0, 0)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !reflect.DeepEqual(executed, test.wantExecuted) {
				t.Fatalf("executed = %v, want %v", executed, test.wantExecuted)
			}
			if response.Type != test.wantType || len(response.Results) != len(test.wantExecuted) {
				t.Fatalf("Run() type = %s, results = %v", response.Type, response.Results)
			}
			var last Response
			if err := json.Unmarshal([]byte(test.responses[len(test.responses)-1]), &last); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(response.FunctionCalls, last.FunctionCalls) {
				t.Fatalf("Run() calls = %v, want the unexecuted %v", response.FunctionCalls, last.FunctionCalls)
			}
			if len(fake.responses) != 0 || len(fake.inputs) != len(test.responses) {
				t.Fatalf("completions = %d, queued = %d", len(fake.inputs), len(fake.responses))
			}
		})
	}
}

func TestRunTracksToolsPerRun(t *testing.T) {
	t.Parallel()

	turn := []byte(`{"type":"call","function_calls":[{"name":"event","arguments":{"title":"A"}}]}`)
	fake := &fakeNative{responses: [][]byte{turn, []byte(`{"type":"respond"}`), turn, []byte(`{"type":"respond"}`)}}
	executed := 0
	agent, _ := newTestAgent(t, fake, Config{Tools: []Tool{{
		Schema: ToolSchema{Name: "event"},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			executed++
			return "ok", nil
		},
	}}})
	for range 2 {
		if response, err := agent.Run(context.Background(), "add it", 0, 0); err != nil || response.Type != ResponseRespond {
			t.Fatalf("Run() = %s, %v", response.Type, err)
		}
	}
	if executed != 2 {
		t.Fatalf("executed = %d, want 2", executed)
	}
}

func TestRunValidatesRepeatedTurn(t *testing.T) {
	t.Parallel()

	fake := &fakeNative{responses: [][]byte{
		[]byte(`{"type":"call","function_calls":[{"name":"event","arguments":{"title":"A"}}]}`),
		[]byte(`{"type":"call","function_calls":[{"name":"event","arguments":{"title":"B"}}],"validation":{"ungrounded":["event.title"]}}`),
	}}
	agent, _ := newTestAgent(t, fake, Config{Tools: []Tool{{
		Schema:  ToolSchema{Name: "event"},
		Handler: func(context.Context, json.RawMessage) (any, error) { return "ok", nil },
	}}})
	response, err := agent.Run(context.Background(), "add it", 0, 0)
	if !errors.Is(err, ErrValidation) || len(response.Results) != 1 {
		t.Fatalf("Run() results = %v, error = %v; want one result and ErrValidation", response.Results, err)
	}
}
