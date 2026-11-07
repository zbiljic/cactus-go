package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zbiljic/cactus-go"
)

type fakeAgent struct {
	responses []cactus.Response
	inputs    []string
	tokens    []int
	resets    int
	err       error
}

func (a *fakeAgent) Complete(_ context.Context, input string, tokens int) (cactus.Response, error) {
	a.inputs = append(a.inputs, input)
	a.tokens = append(a.tokens, tokens)
	if a.err != nil {
		return cactus.Response{}, a.err
	}
	if len(a.responses) == 0 {
		return cactus.Response{Type: cactus.ResponseRespond, Success: true}, nil
	}
	response := a.responses[0]
	a.responses = a.responses[1:]
	return response, nil
}

func (a *fakeAgent) Reset(context.Context) error {
	a.resets++
	return a.err
}

func testApplication(input string) (*application, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := &application{
		stdin:  strings.NewReader(input),
		stdout: stdout,
		stderr: stderr,
		deps: dependencies{
			newAgent: func(context.Context, cactus.Config) (session, error) {
				return &fakeAgent{}, nil
			},
			fetchEngine: func(context.Context, cactus.FetchOptions) (string, error) {
				return "/cache/libneedle", nil
			},
			cachedEngine: func(cactus.FetchOptions) (string, error) {
				return "", cactus.ErrEngineNotFound
			},
			currentPlatform: func() (cactus.Platform, error) {
				return cactus.PlatformDarwinARM64, nil
			},
		},
	}
	return app, stdout, stderr
}

func TestHelpAndMissingCommand(t *testing.T) {
	t.Parallel()

	app, _, stderr := testApplication("")
	if code := app.run(context.Background(), []string{"help"}); code != 0 {
		t.Fatalf("help exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "doctor") ||
		!strings.Contains(stderr.String(), "eval") ||
		!strings.Contains(stderr.String(), "test") {
		t.Fatalf("help output = %q", stderr.String())
	}

	app, _, stderr = testApplication("")
	if code := app.run(context.Background(), nil); code != 2 {
		t.Fatalf("missing command exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage: cactusz") {
		t.Fatalf("missing command output = %q", stderr.String())
	}
}

func TestFetch(t *testing.T) {
	t.Parallel()

	app, stdout, _ := testApplication("")
	var gotOptions cactus.FetchOptions
	app.deps.fetchEngine = func(_ context.Context, options cactus.FetchOptions) (string, error) {
		gotOptions = options
		return "/tmp/libneedle.so", nil
	}
	code := app.run(context.Background(), []string{
		"fetch",
		"--generation", "2",
		"--platform", "linux-arm64",
		"--cache", "/tmp/engines",
	})
	if code != 0 {
		t.Fatalf("fetch exit code = %d", code)
	}
	if gotOptions.Generation != 2 ||
		gotOptions.Platform != cactus.PlatformLinuxARM64 ||
		gotOptions.CacheDir != "/tmp/engines" {
		t.Fatalf("fetch options = %#v", gotOptions)
	}
	if stdout.String() != "/tmp/libneedle.so\n" {
		t.Fatalf("fetch output = %q", stdout.String())
	}
}

func TestFetchListDoesNotDownload(t *testing.T) {
	t.Parallel()

	app, stdout, _ := testApplication("")
	app.deps.fetchEngine = func(context.Context, cactus.FetchOptions) (string, error) {
		t.Fatal("fetch called with --list")
		return "", nil
	}
	if code := app.run(context.Background(), []string{"fetch", "--list"}); code != 0 {
		t.Fatalf("fetch --list exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), string(cactus.PlatformWindowsARM64)) {
		t.Fatalf("fetch --list output = %q", stdout.String())
	}
}

func TestComplete(t *testing.T) {
	t.Parallel()

	toolsPath := filepath.Join(t.TempDir(), "tools.json")
	tools := `[{
		"name":"weather",
		"parameters":{"type":"object","properties":{"city":{"type":"string"}}}
	}]`
	if err := os.WriteFile(toolsPath, []byte(tools), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeAgent{responses: []cactus.Response{{
		Type:    cactus.ResponseCall,
		Success: true,
		FunctionCalls: []cactus.FunctionCall{{
			Name:      "weather",
			Arguments: json.RawMessage(`{"city":"Lagos"}`),
		}},
	}}}
	app, stdout, stderr := testApplication("")
	var gotConfig cactus.Config
	app.deps.newAgent = func(_ context.Context, config cactus.Config) (session, error) {
		gotConfig = config
		return fake, nil
	}
	code := app.run(context.Background(), []string{
		"complete",
		"--generation", "2",
		"--tools", toolsPath,
		"--system", "device: phone",
		"--max-tokens", "64",
		"--prompt", "weather in Lagos",
	})
	if code != 0 {
		t.Fatalf("complete exit code = %d, stderr = %q", code, stderr.String())
	}
	if len(gotConfig.Tools) != 1 || gotConfig.Tools[0].Schema.Name != "weather" {
		t.Fatalf("agent tools = %#v", gotConfig.Tools)
	}
	if gotConfig.Generation != 2 || gotConfig.System != "device: phone" {
		t.Fatalf("agent system = %q", gotConfig.System)
	}
	if len(fake.inputs) != 1 || fake.inputs[0] != "weather in Lagos" || fake.tokens[0] != 64 {
		t.Fatalf("completion calls = %#v tokens = %#v", fake.inputs, fake.tokens)
	}
	if !strings.Contains(stdout.String(), `"weather"`) || !json.Valid(stdout.Bytes()) {
		t.Fatalf("complete output = %q", stdout.String())
	}
}

func TestCompleteValidationJSON(t *testing.T) {
	t.Parallel()

	fake := &fakeAgent{responses: []cactus.Response{{
		Type:       cactus.ResponseCall,
		Validation: &cactus.Validation{Ungrounded: []string{"weather.city"}, Negation: true},
	}}}
	app, stdout, stderr := testApplication("")
	app.deps.newAgent = func(context.Context, cactus.Config) (session, error) { return fake, nil }
	if code := app.run(context.Background(), []string{"complete", "--prompt", "weather"}); code != 0 {
		t.Fatalf("complete exit code = %d, stderr = %q", code, stderr.String())
	}
	var response cactus.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Validation == nil || !response.Validation.Negation || !reflect.DeepEqual(response.Validation.Ungrounded, []string{"weather.city"}) {
		t.Fatalf("validation = %#v", response.Validation)
	}
}

func TestCompleteReadsStdin(t *testing.T) {
	t.Parallel()

	fake := &fakeAgent{}
	app, _, stderr := testApplication("extract this invoice\n")
	app.deps.newAgent = func(context.Context, cactus.Config) (session, error) {
		return fake, nil
	}
	if code := app.run(context.Background(), []string{"complete"}); code != 0 {
		t.Fatalf("complete exit code = %d, stderr = %q", code, stderr.String())
	}
	if got := fake.inputs[0]; got != "extract this invoice\n" {
		t.Fatalf("stdin prompt = %q", got)
	}
}

func TestREPL(t *testing.T) {
	t.Parallel()

	fake := &fakeAgent{responses: []cactus.Response{
		{Type: cactus.ResponseCall},
		{Type: cactus.ResponseRespond},
	}}
	app, stdout, stderr := testApplication("first\n.reset\nsecond\n.exit\n")
	app.deps.newAgent = func(context.Context, cactus.Config) (session, error) {
		return fake, nil
	}
	if code := app.run(context.Background(), []string{"repl"}); code != 0 {
		t.Fatalf("repl exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := fake.inputs, []string{"first", "second"}; !equalStrings(got, want) {
		t.Fatalf("repl inputs = %#v, want %#v", got, want)
	}
	if fake.resets != 1 {
		t.Fatalf("repl resets = %d", fake.resets)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("repl output = %q", stdout.String())
	}
}

func TestDoctor(t *testing.T) {
	t.Parallel()

	libraryPath := filepath.Join(t.TempDir(), "libneedle.dylib")
	if err := os.WriteFile(libraryPath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeAgent{responses: []cactus.Response{{
		Type:       cactus.ResponseCall,
		PrefillTPS: 100,
		PeakRAMMB:  22,
	}}}
	app, stdout, stderr := testApplication("")
	cacheDir := t.TempDir()
	app.deps.newAgent = func(_ context.Context, config cactus.Config) (session, error) {
		if config.LibraryPath != libraryPath ||
			config.CacheDir != cacheDir ||
			config.Generation != 3 {
			t.Fatalf("doctor config = %+v", config)
		}
		return fake, nil
	}
	if code := app.run(context.Background(), []string{
		"doctor",
		"--library", libraryPath,
		"--cache", cacheDir,
		"--smoke",
	}); code != 0 {
		t.Fatalf("doctor exit code = %d, stderr = %q", code, stderr.String())
	}
	if fake.resets != 1 || len(fake.inputs) != 1 {
		t.Fatalf("doctor resets = %d inputs = %#v", fake.resets, fake.inputs)
	}
	if !strings.Contains(stdout.String(), "✓ Smoke test") {
		t.Fatalf("doctor output = %q", stdout.String())
	}
}

func TestDoctorMissingLibrary(t *testing.T) {
	t.Parallel()

	app, stdout, stderr := testApplication("")
	if code := app.run(context.Background(), []string{"doctor"}); code != 1 {
		t.Fatalf("doctor exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "✗ Library: not found") ||
		!strings.Contains(stderr.String(), "cactusz fetch") {
		t.Fatalf("doctor stdout = %q stderr = %q", stdout.String(), stderr.String())
	}
}

func TestConformance(t *testing.T) {
	t.Parallel()

	weatherCall := func(city, day string) cactus.Response {
		arguments, err := json.Marshal(map[string]string{"city": city, "day": day})
		if err != nil {
			t.Fatal(err)
		}
		return cactus.Response{
			Type:    cactus.ResponseCall,
			Success: true,
			FunctionCalls: []cactus.FunctionCall{{
				Name:      "get_weather",
				Arguments: arguments,
			}},
		}
	}
	respond := cactus.Response{Type: cactus.ResponseRespond, Success: true}
	weatherAgents := 0
	var configs []cactus.Config

	app, stdout, stderr := testApplication("")
	app.deps.newAgent = func(_ context.Context, config cactus.Config) (session, error) {
		configs = append(configs, config)
		if len(config.Tools) == 0 {
			return &fakeAgent{responses: []cactus.Response{{Type: cactus.ResponseCall, Success: true}}}, nil
		}
		switch config.Tools[0].Schema.Name {
		case "invoice":
			return &fakeAgent{responses: []cactus.Response{{
				Type:    cactus.ResponseCall,
				Success: true,
				FunctionCalls: []cactus.FunctionCall{{
					Name: "invoice",
					Arguments: json.RawMessage(
						`{"vendor":"Acme Corp","total":1200,"due_date":"2026-09-01"}`,
					),
				}},
			}}}, nil
		case "get_weather":
			if config.System != "" {
				return &fakeAgent{responses: []cactus.Response{weatherCall("London", "today")}}, nil
			}
			weatherAgents++
			switch weatherAgents {
			case 1:
				return &fakeAgent{responses: []cactus.Response{weatherCall("Tokyo", "tomorrow")}}, nil
			case 2:
				return &fakeAgent{responses: []cactus.Response{weatherCall("Paris", "tomorrow"), respond}}, nil
			case 3:
				return &fakeAgent{responses: []cactus.Response{
					weatherCall("Paris", "tomorrow"),
					respond,
					weatherCall("Paris", "tomorrow"),
				}}, nil
			}
		}
		return nil, errors.New("unexpected conformance configuration")
	}

	code := app.run(context.Background(), []string{
		"test",
		"--generation", "2",
		"--library", "/tmp/libneedle",
		"--cache", "/tmp/cactus-cache",
		"--buffer-size", "4096",
	})
	if code != 0 {
		t.Fatalf("test exit code = %d, stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "ALL 6 CHECKS PASSED") ||
		!strings.Contains(stdout.String(), "structured extraction") {
		t.Fatalf("test output = %q", stdout.String())
	}
	if len(configs) != 6 {
		t.Fatalf("agent configs = %d, want 6", len(configs))
	}
	for _, config := range configs {
		if config.Generation != 2 || config.LibraryPath != "/tmp/libneedle" ||
			config.CacheDir != "/tmp/cactus-cache" ||
			config.BufferSize != 4096 {
			t.Fatalf("agent config = %#v", config)
		}
	}
}

func TestConformanceFailure(t *testing.T) {
	t.Parallel()

	app, stdout, stderr := testApplication("")
	app.deps.newAgent = func(context.Context, cactus.Config) (session, error) {
		return nil, errors.New("broken runtime")
	}
	if code := app.run(context.Background(), []string{"test"}); code != 1 {
		t.Fatalf("test exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "6 OF 6 CHECKS FAILED") ||
		!strings.Contains(stderr.String(), "conformance checks failed") {
		t.Fatalf("test stdout = %q stderr = %q", stdout.String(), stderr.String())
	}

	app, _, _ = testApplication("")
	if code := app.run(context.Background(), []string{"test", "unexpected"}); code != 2 {
		t.Fatalf("test usage exit code = %d", code)
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	app, stdout, stderr := testApplication("")
	if code := app.run(context.Background(), []string{"version"}); code != 0 {
		t.Fatalf("version exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cactusz ") ||
		!strings.Contains(stdout.String(), "engine "+cactus.EngineVersion) ||
		!strings.Contains(stdout.String(), string(cactus.PlatformDarwinARM64)) {
		t.Fatalf("version output = %q", stdout.String())
	}
}

func TestDoctorWeightsSelectGeneration(t *testing.T) {
	t.Setenv("NEEDLE_LIB_PATH", "")
	t.Setenv("NEEDLE2_LIB_PATH", "")
	t.Setenv("CACTUS_LIB_PATH", "")
	cache := t.TempDir()
	weights := filepath.Join(cache, "custom.cact")
	if err := os.WriteFile(weights, []byte{0x83, 0x2a, 0xe1, 0x05}, 0o600); err != nil {
		t.Fatal(err)
	}
	app, stdout, stderr := testApplication("")
	app.deps.cachedEngine = func(options cactus.FetchOptions) (string, error) {
		if options.Generation != 2 || options.CacheDir != cache {
			t.Fatalf("cache options=%+v", options)
		}
		return "/cache/libneedle", nil
	}
	app.deps.newAgent = func(_ context.Context, config cactus.Config) (session, error) {
		if config.Generation != 2 ||
			config.CacheDir != cache ||
			config.WeightsPath != weights {
			t.Fatalf("config=%+v", config)
		}
		return &fakeAgent{}, nil
	}
	if code := app.run(context.Background(), []string{
		"doctor",
		"--generation", "3",
		"--weights", weights,
		"--cache", cache,
	}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "2.0.4") {
		t.Fatalf("version output=%s", stdout.String())
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
