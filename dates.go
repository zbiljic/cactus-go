package cactus

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Date grounding follows upstream's client-side check: a date argument must
// carry a year written in the conversation. The agent collects the years of
// every input since the conversation began. When the current input reasons
// relatively, such as "tomorrow" or "next Friday", the years of the system
// facts, including the date fact, count too. Without any written year nothing
// is checked.

// yearSpace and yearDigit match what Python's Unicode \s and \d match.
const (
	yearSpace = `[\s\v\x{1c}-\x{1f}\x{85}\p{Z}]`
	yearDigit = `\p{Nd}`
	months    = `(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|` +
		`sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?`
	ordinal = `(?:st|nd|rd|th)?`
)

// yearPattern finds the text before a year, anchored where the scan stands.
// The year itself, of minDigits to maxDigits digits, follows the match and is
// read by yearAt, which also applies the lookahead.
type yearPattern struct {
	prefix               *regexp.Regexp
	minDigits, maxDigits int
	// afterWord requires the match to start after a non-word rune, as \b does.
	afterWord bool
	// afterASCIIDigit requires the match to start after a non-[0-9] rune.
	afterASCIIDigit bool
	accept          func(rest string) bool
}

var yearPatterns = []yearPattern{
	{
		prefix:    regexp.MustCompile(`^` + yearDigit + `{1,2}` + ordinal + yearSpace + `+` + months + `[,` + yearSpace[1:len(yearSpace)-1] + `]+`),
		minDigits: 1, maxDigits: 4, afterWord: true, accept: notTimeOrWord,
	},
	{
		prefix: regexp.MustCompile(`^` + months + yearSpace + `+` + yearDigit + `{1,2}` + ordinal +
			yearSpace + `*,?` + yearSpace + `+`),
		minDigits: 1, maxDigits: 4, afterWord: true, accept: notTimeOrWord,
	},
	{
		prefix:    regexp.MustCompile(`^` + months + `[,` + yearSpace[1:len(yearSpace)-1] + `]+`),
		minDigits: 3, maxDigits: 4, afterWord: true, accept: notASCIIWord,
	},
	{
		prefix:    regexp.MustCompile(`^year` + yearSpace + `+`),
		minDigits: 1, maxDigits: 4, afterWord: true, accept: notASCIIWord,
	},
	{
		prefix:    regexp.MustCompile(`^`),
		minDigits: 4, maxDigits: 4, afterASCIIDigit: true, accept: numericDateRest,
	},
}

var (
	timeAfterYear = regexp.MustCompile(`^(?::` + yearDigit + `|` + yearSpace + `*[ap]\.?m)`)
	dateRest      = regexp.MustCompile(`^[-/]` + yearDigit + `{1,2}[-/]`)
	relativeCue   = regexp.MustCompile(`^(?i:today|tonight|tomorrow|yesterday|next|this|coming|now|` +
		`in ` + yearDigit + `+ (?:days?|weeks?|months?|years?)|monday|tuesday|wednesday|thursday|friday|saturday|sunday)`)
	dateValueYear = regexp.MustCompile(`^(` + yearDigit + `{4})-`)
)

// sourceYears returns the years written in text, as upstream reads them.
func sourceYears(text string) map[int]bool {
	text = pythonLower(text)
	years := make(map[int]bool)
	for _, pattern := range yearPatterns {
		for index := 0; index < len(text); {
			year, end, ok := pattern.yearAt(text, index)
			if !ok {
				_, size := utf8.DecodeRuneInString(text[index:])
				index += size
				continue
			}
			years[year] = true
			index = end
		}
	}
	return years
}

// yearAt matches pattern at index and returns the year and where it ends.
// Like a backtracking regular expression, it takes the most digits the
// lookahead accepts.
func (p yearPattern) yearAt(text string, index int) (int, int, bool) {
	previous, _ := utf8.DecodeLastRuneInString(text[:index])
	if index > 0 && p.afterWord && isWordRune(previous) {
		return 0, 0, false
	}
	if index > 0 && p.afterASCIIDigit && previous >= '0' && previous <= '9' {
		return 0, 0, false
	}
	match := p.prefix.FindStringIndex(text[index:])
	if match == nil {
		return 0, 0, false
	}
	start := index + match[1]
	ends := digitEnds(text[start:], p.maxDigits)
	for count := len(ends); count >= p.minDigits; count-- {
		end := start + ends[count-1]
		if p.accept(text[end:]) {
			return digitValue(text[start:end]), end, true
		}
	}
	return 0, 0, false
}

// digitEnds returns the byte offset after each of the first max digits.
func digitEnds(text string, max int) []int {
	var ends []int
	offset := 0
	for len(ends) < max {
		value, size := utf8.DecodeRuneInString(text[offset:])
		if size == 0 || !unicode.IsDigit(value) {
			break
		}
		offset += size
		ends = append(ends, offset)
	}
	return ends
}

// digitValue reads decimal digits of any script, as Python's int does.
func digitValue(digits string) int {
	value := 0
	for _, digit := range digits {
		value = value*10 + digitOf(digit)
	}
	return value
}

// digitOf returns the value of a decimal digit. Unicode encodes decimal
// digits in runs of whole 0 to 9 blocks, so the offset from the start of the
// run gives the value.
func digitOf(digit rune) int {
	start := digit
	for unicode.IsDigit(start - 1) {
		start--
	}
	return int(digit-start) % 10
}

// notASCIIWord is the lookahead (?![0-9A-Za-z]).
func notASCIIWord(rest string) bool {
	return rest == "" || !isASCIIWord(rest[0])
}

// notTimeOrWord is the lookahead (?![0-9A-Za-z]|:\d|\s*[ap]\.?m\b).
func notTimeOrWord(rest string) bool {
	if !notASCIIWord(rest) {
		return false
	}
	match := timeAfterYear.FindStringIndex(rest)
	if match == nil {
		return true
	}
	if rest[match[1]-1] != 'm' {
		return false
	}
	next, _ := utf8.DecodeRuneInString(rest[match[1]:])
	return rest[match[1]:] != "" && isWordRune(next)
}

// numericDateRest is the lookahead (?=[-/]\d{1,2}[-/]\d{1,2}(?![0-9])).
func numericDateRest(rest string) bool {
	match := dateRest.FindStringIndex(rest)
	if match == nil {
		return false
	}
	day := rest[match[1]:]
	for _, end := range digitEnds(day, 2) {
		if end == len(day) || day[end] < '0' || day[end] > '9' {
			return true
		}
	}
	return false
}

func isASCIIWord(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

// pythonLower lowers text as Python's str.lower does. Python maps İ to i and
// a combining dot above, where Go maps it to a plain i; no other letter
// differs.
func pythonLower(text string) string {
	return strings.ToLower(strings.ReplaceAll(text, "İ", "i\u0307"))
}

// pythonFold makes Go's case-insensitive matching treat İ and ı as i, as
// Python's IGNORECASE does.
var pythonFold = strings.NewReplacer("İ", "i", "ı", "i")

// hasRelativeCue reports whether text reasons relative to the date fact.
func hasRelativeCue(text string) bool {
	text = pythonFold.Replace(text)
	for index := 0; index < len(text); {
		previous, _ := utf8.DecodeLastRuneInString(text[:index])
		if index == 0 || !isWordRune(previous) {
			if match := relativeCue.FindStringIndex(text[index:]); match != nil {
				next, _ := utf8.DecodeRuneInString(text[index+match[1]:])
				if index+match[1] == len(text) || !isWordRune(next) {
					return true
				}
			}
		}
		_, size := utf8.DecodeRuneInString(text[index:])
		index += size
	}
	return false
}

// dateSchemas maps each tool name to its parameters, decoded from the tools
// JSON sent to the engine.
func dateSchemas(toolsJSON []byte) map[string]map[string]any {
	var tools []struct {
		Name       string         `json:"name"`
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(toolsJSON, &tools); err != nil {
		return nil
	}
	schemas := make(map[string]map[string]any, len(tools))
	for _, tool := range tools {
		schemas[tool.Name] = tool.Parameters
	}
	return schemas
}

// groundDates adds to response the date arguments whose year is not licensed:
// written in the conversation, or in the system facts when text reasons
// relatively. It does nothing when the conversation has no year.
func groundDates(response *Response, schemas map[string]map[string]any, seen map[int]bool, system, text string) {
	if len(response.FunctionCalls) == 0 || len(seen) == 0 {
		return
	}
	years := seen
	if system != "" && hasRelativeCue(text) {
		years = make(map[int]bool, len(seen))
		for year := range seen {
			years[year] = true
		}
		for year := range sourceYears(system) {
			years[year] = true
		}
	}
	var found []string
	for _, call := range response.FunctionCalls {
		root, ok := schemas[call.Name]
		if !ok {
			continue
		}
		var arguments any
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			continue
		}
		for _, path := range unlicensedDates(root, arguments, years) {
			found = append(found, call.Name+"."+path)
		}
	}
	if len(found) == 0 {
		return
	}
	if response.Validation == nil {
		response.Validation = &Validation{}
	}
	for _, name := range found {
		if !containsString(response.Validation.Ungrounded, name) {
			response.Validation.Ungrounded = append(response.Validation.Ungrounded, name)
		}
	}
}

// unlicensedDates walks arguments along the schema and returns the sorted
// paths of date and date-time strings whose year is not in years.
func unlicensedDates(root map[string]any, arguments any, years map[int]bool) []string {
	var failures []string
	var walk func(value any, node map[string]any, path string)
	walk = func(value any, node map[string]any, path string) {
		node = resolveRef(node, root)
		if variants := concreteVariants(node, root); len(variants) == 1 {
			node = variants[0]
		}
		if format, _ := node["format"].(string); format == "date" || format == "date-time" {
			if text, ok := value.(string); ok {
				if match := dateValueYear.FindStringSubmatch(text); match != nil && !years[digitValue(match[1])] {
					failures = append(failures, path)
				}
				return
			}
		}
		switch value := value.(type) {
		case map[string]any:
			properties, _ := node["properties"].(map[string]any)
			for key, item := range value {
				child, ok := properties[key].(map[string]any)
				if !ok {
					if _, declared := properties[key]; !declared {
						continue
					}
					child = map[string]any{}
				}
				childPath := key
				if path != "" {
					childPath = path + "." + key
				}
				walk(item, child, childPath)
			}
		case []any:
			items, ok := node["items"].(map[string]any)
			if _, declared := node["items"]; !declared {
				return
			}
			if !ok {
				items = map[string]any{}
			}
			for index, item := range value {
				walk(item, items, path+"["+strconv.Itoa(index)+"]")
			}
		}
	}
	walk(arguments, root, "")
	slices.Sort(failures)
	return failures
}

// resolveRef follows local "#/..." references from root, stopping at a cycle.
func resolveRef(node, root map[string]any) map[string]any {
	seen := make(map[string]bool)
	for {
		ref, ok := node["$ref"].(string)
		if !ok || seen[ref] {
			return node
		}
		seen[ref] = true
		target := root
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			next, _ := target[part].(map[string]any)
			if next == nil {
				next = map[string]any{}
			}
			target = next
		}
		node = target
	}
}

// concreteVariants returns the anyOf (or oneOf) variants that are not null.
func concreteVariants(node, root map[string]any) []map[string]any {
	variants, _ := node["anyOf"].([]any)
	if len(variants) == 0 {
		variants, _ = node["oneOf"].([]any)
	}
	var concrete []map[string]any
	for _, variant := range variants {
		variant, ok := variant.(map[string]any)
		if !ok {
			continue
		}
		variant = resolveRef(variant, root)
		if variant["type"] != "null" {
			concrete = append(concrete, variant)
		}
	}
	return concrete
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
