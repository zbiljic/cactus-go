package cactus

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestValidateResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		validation *Validation
		want       string
	}{
		{name: "absent"},
		{name: "empty", validation: &Validation{}},
		{name: "negation false", validation: &Validation{Negation: false}},
		{name: "negation first", validation: &Validation{Negation: true, Ungrounded: []string{"invoice.total"}}, want: "negation"},
		{name: "ungrounded supplied order", validation: &Validation{Ungrounded: []string{"invoice.total", "unknown.path"}}, want: "invoice.total, unknown.path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := Response{Validation: test.validation, FunctionCalls: []FunctionCall{{Name: "invoice", Arguments: []byte(`{"total":1200}`)}}}
			beforeArguments := append(json.RawMessage(nil), response.FunctionCalls[0].Arguments...)
			var beforeUngrounded []string
			if test.validation != nil {
				beforeUngrounded = append([]string(nil), test.validation.Ungrounded...)
			}
			err := ValidateResponse(response)
			if (test.want == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateResponse() error = %v, want %q", err, test.want)
			}
			if !reflect.DeepEqual(response.FunctionCalls[0].Arguments, beforeArguments) || test.validation != nil && !reflect.DeepEqual(test.validation.Ungrounded, beforeUngrounded) {
				t.Fatalf("ValidateResponse() mutated response: %#v", response)
			}
		})
	}
}

func TestValidateResponseSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		calls      string
		suppressed string
		flagged    string
		sources    []string
		wantError  string
	}{
		{name: "fraction written with trailing zero", calls: `[{"name":"pay","arguments":{"amount":25.5}}]`, flagged: "pay.amount", sources: []string{"send $25.50 to Tom"}},
		{name: "thousands separator", calls: `[{"name":"pay","arguments":{"amount":1234}}]`, flagged: "pay.amount", sources: []string{"send 1,234 dollars"}},
		{name: "misread thousands separator", calls: `[{"name":"pay","arguments":{"amount":1.234}}]`, flagged: "pay.amount", sources: []string{"send 1,234 dollars"}, wantError: "pay.amount"},
		{name: "second source", calls: `[{"name":"pay","arguments":{"amount":40}}]`, flagged: "pay.amount", sources: []string{"pay the usual", "usual: 40"}},
		{name: "negative number", calls: `[{"name":"set","arguments":{"offset":-3}}]`, flagged: "set.offset", sources: []string{"shift by -3"}},
		{name: "nested path", calls: `[{"name":"order","arguments":{"items":[{"qty":2},{"qty":12}]}}]`, flagged: "order.items[1].qty", sources: []string{"two apples and 12 pears"}},
		{name: "suppressed call", suppressed: `[{"name":"pay","arguments":{"amount":9}}]`, flagged: "pay.amount", sources: []string{"pay 9"}},
		{name: "unsourced suppressed call", calls: `[{"name":"pay","arguments":{"amount":9}}]`, suppressed: `[{"name":"pay","arguments":{"amount":7}}]`, flagged: "pay.amount", sources: []string{"pay 9"}, wantError: "pay.amount"},
		{name: "dotted tool name", calls: `[{"name":"pay.amount","arguments":{"value":7}},{"name":"pay","arguments":{"amount":{"value":9}}}]`, flagged: "pay.amount.value", sources: []string{"pay 9"}, wantError: "pay.amount.value"},
		{name: "unique dotted tool name", calls: `[{"name":"pay.v2","arguments":{"amount":9}}]`, flagged: "pay.v2.amount", sources: []string{"pay 9"}},
		{name: "dotted argument key", calls: `[{"name":"t","arguments":{"a":{"b":7},"a.b":9}}]`, flagged: "t.a.b", sources: []string{"use 9"}, wantError: "t.a.b"},
		{name: "non-ASCII digit after number", calls: `[{"name":"pay","arguments":{"amount":50}}]`, flagged: "pay.amount", sources: []string{"send 50\u0663"}, wantError: "pay.amount"},
		{name: "superscript before number", calls: `[{"name":"pay","arguments":{"amount":61}}]`, flagged: "pay.amount", sources: []string{"x\u00b261"}, wantError: "pay.amount"},
		{name: "date fact with vertical tab", calls: `[{"name":"book","arguments":{"day":25}}]`, flagged: "book.day", sources: []string{"date:\u000b2026-11-25 Wed 14:30"}, wantError: "book.day"},
		{name: "date fact with unit separator", calls: `[{"name":"book","arguments":{"day":25}}]`, flagged: "book.day", sources: []string{"date:\u001f2026-11-25 Wed 14:30"}, wantError: "book.day"},
		{name: "date fact with non-ASCII digit", calls: `[{"name":"book","arguments":{"day":11}}]`, flagged: "book.day", sources: []string{"date: 2026-11-2\u0665 Wed 14:30"}, wantError: "book.day"},
		{name: "hyphen after non-ASCII digit", calls: `[{"name":"set","arguments":{"offset":-5}}]`, flagged: "set.offset", sources: []string{"\u0661-5"}, wantError: "set.offset"},
		{name: "date fact with no-break space", calls: `[{"name":"book","arguments":{"day":25}}]`, flagged: "book.day", sources: []string{"date:\u00a02026-11-25 Wed 14:30"}, wantError: "book.day"},
		{name: "number inside word", calls: `[{"name":"pay","arguments":{"amount":12}}]`, flagged: "pay.amount", sources: []string{"pay A12"}, wantError: "pay.amount"},
		{name: "number after decimal point", calls: `[{"name":"pay","arguments":{"amount":5}}]`, flagged: "pay.amount", sources: []string{"pay 0.5"}, wantError: "pay.amount"},
		{name: "date fact", calls: `[{"name":"book","arguments":{"day":25}}]`, flagged: "book.day", sources: []string{"book it", "date: 2026-11-25 Wed 14:30"}, wantError: "book.day"},
		{name: "ISO timestamp", calls: `[{"name":"book","arguments":{"day":25}}]`, flagged: "book.day", sources: []string{"at 2026-11-25T14:30"}, wantError: "book.day"},
		{name: "path without tool", calls: `[{"name":"pay","arguments":{"amount":9}}]`, flagged: "amount", sources: []string{"pay 9"}, wantError: "amount"},
		{name: "boolean", calls: `[{"name":"set","arguments":{"on":true}}]`, flagged: "set.on", sources: []string{"turn it on 1"}, wantError: "set.on"},
		{name: "no sources", calls: `[{"name":"pay","arguments":{"amount":9}}]`, flagged: "pay.amount", wantError: "pay.amount"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := Response{Type: ResponseCall, Validation: &Validation{Ungrounded: []string{test.flagged}}}
			for _, calls := range []struct {
				text   string
				target *[]FunctionCall
			}{{test.calls, &response.FunctionCalls}, {test.suppressed, &response.SuppressedCalls}} {
				if calls.text == "" {
					continue
				}
				if err := json.Unmarshal([]byte(calls.text), calls.target); err != nil {
					t.Fatal(err)
				}
			}
			err := ValidateResponse(response, test.sources...)
			if (test.wantError == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateResponse() error = %v, want %q", err, test.wantError)
			}
			if err != nil && !errors.Is(err, ErrValidation) {
				t.Fatalf("ValidateResponse() error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestNumericLeavesSharedPath(t *testing.T) {
	t.Parallel()

	leaves := numericLeaves(json.RawMessage(`{"a":{"b":7},"a.b":9,"c":[1,{"d":2}]}`))
	if number, ok := leaves["a.b"]; !ok || number != nil {
		t.Fatalf(`numericLeaves()["a.b"] = %v, %v; want nil, true`, number, ok)
	}
	for path, want := range map[string]int64{"c[0]": 1, "c[1].d": 2} {
		if number := leaves[path]; number == nil || !number.IsInt() || number.Num().Int64() != want {
			t.Fatalf("numericLeaves()[%q] = %v, want %d", path, number, want)
		}
	}
}

func TestSourceNumbers(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"$1,200.00 and order ID 1200":     {"1200", "1200"},
		"1,2345":                          {"1"},
		"1,234,56":                        {"1234"},
		"+7 or -0.25":                     {"7", "-1/4"},
		"room 4B, floor 3.":               {"4", "3"},
		"x12 a_3 3.5.6 é9":                {"7/2"},
		"date: 2026-11-25 Wed 14:30 at 9": {"9"},
		"1,234.5x 99,999 1234,567":        {"2469/2", "99999", "1234"},
	}
	for source, want := range tests {
		var got []string
		for _, number := range sourceNumbers([]string{source}) {
			got = append(got, number.RatString())
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sourceNumbers(%q) = %v, want %v", source, got, want)
		}
	}
}
