package cactus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateResponse checks only the response's validation warnings, the
// engine's and the dates Complete flags, accepting absent metadata. It rejects
// negation and ungrounded field paths.
//
// Sources are the texts the request was built from, usually the user's input
// and the system facts. The engine can flag a number the user did write, such
// as 25.5 for "$25.50", so an ungrounded number is accepted when every call of
// that tool carries, at that path, a value written in a source. Thousands
// separators are ignored, so "1,234" grounds 1234 but not 1.234. Date facts in
// a source ground nothing. Without sources every warning is rejected.
// Unflagged output still needs application validation.
func ValidateResponse(response Response, sources ...string) error {
	if response.Validation == nil {
		return nil
	}
	if response.Validation.Negation {
		return fmt.Errorf("%w: negation detected", ErrValidation)
	}
	ungrounded := response.Validation.Ungrounded
	if len(ungrounded) > 0 && len(sources) > 0 {
		ungrounded = withoutSourcedNumbers(response, ungrounded, sources)
	}
	if len(ungrounded) > 0 {
		return fmt.Errorf("%w: ungrounded fields: %s", ErrValidation, strings.Join(ungrounded, ", "))
	}
	return nil
}

// withoutSourcedNumbers drops flagged "tool.path" entries whose number, in
// every call of that tool, is written in a source. An entry that more than
// one tool name could prefix stays flagged.
func withoutSourcedNumbers(response Response, flagged []string, sources []string) []string {
	numbers := sourceNumbers(sources)
	if len(numbers) == 0 {
		return flagged
	}
	calls := append(append([]FunctionCall(nil), response.FunctionCalls...), response.SuppressedCalls...)
	remaining := make([]string, 0, len(flagged))
	for _, entry := range flagged {
		tool, ok := flaggedTool(calls, entry)
		if !ok || !sourcedInEveryCall(calls, tool, entry[len(tool)+1:], numbers) {
			remaining = append(remaining, entry)
		}
	}
	return remaining
}

// flaggedTool returns the one call name that prefixes entry as "name.".
func flaggedTool(calls []FunctionCall, entry string) (string, bool) {
	tool := ""
	for _, call := range calls {
		if call.Name == tool || !strings.HasPrefix(entry, call.Name+".") {
			continue
		}
		if tool != "" {
			return "", false
		}
		tool = call.Name
	}
	return tool, tool != ""
}

func sourcedInEveryCall(calls []FunctionCall, tool, path string, numbers []*big.Rat) bool {
	matched := false
	for _, call := range calls {
		if call.Name != tool {
			continue
		}
		matched = true
		value := numericLeaves(call.Arguments)[path]
		if value == nil || !containsNumber(numbers, value) {
			return false
		}
	}
	return matched
}

// numericLeaves maps each number in arguments to its path, written as
// "key.key[0]". A path that two values share, such as "a.b" for {"a":{"b":1}}
// and {"a.b":2}, maps to nil.
func numericLeaves(arguments json.RawMessage) map[string]*big.Rat {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	leaves := make(map[string]*big.Rat)
	seen := make(map[string]bool)
	var walk func(value any, path string)
	walk = func(value any, path string) {
		if seen[path] {
			leaves[path] = nil
			return
		}
		seen[path] = true
		switch value := value.(type) {
		case json.Number:
			if number, ok := new(big.Rat).SetString(value.String()); ok {
				leaves[path] = number
			}
		case map[string]any:
			for key, item := range value {
				if path != "" {
					key = path + "." + key
				}
				walk(item, key)
			}
		case []any:
			for index, item := range value {
				walk(item, path+"["+strconv.Itoa(index)+"]")
			}
		}
	}
	walk(value, "")
	return leaves
}

func containsNumber(numbers []*big.Rat, value *big.Rat) bool {
	for _, number := range numbers {
		if number.Cmp(value) == 0 {
			return true
		}
	}
	return false
}

// dateFactPattern matches a date fact or an ISO timestamp, with Unicode
// spaces and digits as upstream's Python pattern reads them.
var dateFactPattern = regexp.MustCompile(
	`date:` + dateSpace + `*` + dateDigits(4) + `-` + dateDigits(2) + `-` + dateDigits(2) +
		`(?:` + dateSpace + `+[A-Za-z]{3})?(?:` + dateSpace + `+` + dateDigits(2) + `:` + dateDigits(2) + `)?` +
		`|` + dateDigits(4) + `-` + dateDigits(2) + `-` + dateDigits(2) + `T` + dateDigits(2) + `:` + dateDigits(2) +
		`(?::` + dateDigits(2) + `)?`)

const dateSpace = `[\s\v\x{1c}-\x{1f}\x{85}\p{Z}]`

func dateDigits(count int) string {
	return `\p{Nd}{` + strconv.Itoa(count) + `}`
}

// sourceNumbers returns the numbers written in the sources, outside date
// facts and words.
func sourceNumbers(sources []string) []*big.Rat {
	var numbers []*big.Rat
	for _, source := range sources {
		text := withoutNonASCIINumbers(dateFactPattern.ReplaceAllString(source, " "))
		for index := 0; index < len(text); {
			previous, _ := utf8.DecodeLastRuneInString(text[:index])
			if index > 0 && (isWordRune(previous) || previous == '.' || previous == ',') {
				index++
				continue
			}
			token := numberToken(text[index:])
			if token == "" {
				index++
				continue
			}
			if number, ok := new(big.Rat).SetString(strings.ReplaceAll(token, ",", "")); ok {
				numbers = append(numbers, number)
			}
			index += len(token)
		}
	}
	return numbers
}

// numberToken returns the number at the start of text: a sign, then digits in
// comma-separated thousands or plain, then an optional fraction.
func numberToken(text string) string {
	start := 0
	if start < len(text) && (text[start] == '-' || text[start] == '+') {
		start++
	}
	if token := thousandsToken(text, start); token != "" {
		return token
	}
	end := digitsEnd(text, start)
	if end == start {
		return ""
	}
	return text[:fractionEnd(text, end)]
}

func thousandsToken(text string, start int) string {
	end := digitsEnd(text, start)
	if end == start || end-start > 3 {
		return ""
	}
	groups := 0
	for end+4 <= len(text) && text[end] == ',' && digitsEnd(text, end+1) == end+4 {
		end += 4
		groups++
	}
	if groups == 0 {
		return ""
	}
	end = fractionEnd(text, end)
	if end < len(text) && isDigit(text[end]) {
		return ""
	}
	return text[:end]
}

func fractionEnd(text string, end int) int {
	if end < len(text) && text[end] == '.' {
		if digits := digitsEnd(text, end+1); digits > end+1 {
			return digits
		}
	}
	return end
}

func digitsEnd(text string, index int) int {
	for index < len(text) && isDigit(text[index]) {
		index++
	}
	return index
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isWordRune(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsNumber(value)
}

// withoutNonASCIINumbers blanks each run of digits, commas and points that
// holds a non-ASCII digit, which the scanner would otherwise read in part. It
// fills the run with a letter so that a following hyphen does not read as a
// minus sign.
func withoutNonASCIINumbers(text string) string {
	runes := []rune(text)
	for start := 0; start < len(runes); {
		end := start
		nonASCII := false
		for end < len(runes) && (unicode.IsDigit(runes[end]) || runes[end] == ',' || runes[end] == '.') {
			nonASCII = nonASCII || runes[end] > '9'
			end++
		}
		if nonASCII {
			for index := start; index < end; index++ {
				runes[index] = 'x'
			}
		}
		start = max(end, start+1)
	}
	return string(runes)
}
