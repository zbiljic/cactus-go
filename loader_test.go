//go:build (darwin && !ios) || (linux && !android) || windows

package cactus

import (
	"errors"
	"strings"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestRegisterNativeABI(t *testing.T) {
	for _, generation := range []int{2, 3} {
		prefix, modern := "needle_", generation == 3
		if modern {
			prefix = "cactus_"
		}
		t.Run(prefix+"abi", func(t *testing.T) {
			calls := 0
			var gotInput string
			var gotTokens, gotCapacity, gotSamples int32
			var gotPCM *float32
			transcriptions, audioSettings, streams := 0, 0, 0
			embedCalls, describes := 0, 0
			embed := func(input *byte, output *float32, capacity int32) int32 {
				embedCalls++
				if readCString(input) != "hello" {
					t.Error("wrong embedding input")
				}
				if output != nil {
					if capacity != 2 {
						t.Errorf("embedding capacity = %d", capacity)
					}
					copy(unsafe.Slice(output, int(capacity)), []float32{0.25, -0.75})
				} else if capacity != 0 {
					t.Error("size query has nonzero capacity")
				}
				return 2
			}
			complete := func(input *byte, tokens int32, output *byte, capacity int32) int32 {
				calls++
				gotInput, gotTokens, gotCapacity = readCString(input), tokens, capacity
				copy(unsafe.Slice(output, int(capacity)), "response\x00")
				return 7
			}
			symbols := map[string]uintptr{
				prefix + "init":     purego.NewCallback(func(*byte, *byte, *byte) int32 { return 0 }),
				prefix + "complete": purego.NewCallback(complete),
				prefix + "reset":    purego.NewCallback(func() {}),
				prefix + "load":     purego.NewCallback(func(*byte, uint64) int32 { return 0 }),
			}
			if modern {
				symbols[prefix+"complete"] = purego.NewCallback(func(input *byte, pcm *float32, samples, tokens int32, output *byte, capacity int32) int32 {
					gotPCM, gotSamples = pcm, samples
					return complete(input, tokens, output, capacity)
				})
				symbols[prefix+"models"] = purego.NewCallback(func() int32 { return 7 })
				symbols[prefix+"describe"] = purego.NewCallback(func(rgb *byte, width, height int32, options, language, keywords *byte, output *byte, capacity int32) int32 {
					describes++
					if rgb == nil || *rgb != 9 || width != 1 || height != 1 || readCString(options) != `{"sections":["scene"]}` || readCString(language) != "en" || readCString(keywords) != "Paris\nAda" || capacity != 32 {
						t.Error("incorrect eight-argument describe ABI")
					}
					copy(unsafe.Slice(output, capacity), "vision\x00")
					return 5
				})
				symbols[prefix+"transcribe"] = purego.NewCallback(func(pcm *float32, samples int32, language, keywords *byte, timestamps int32, output *byte, capacity int32) int32 {
					transcriptions++
					if pcm == nil || samples != 2 || *pcm != 0.25 || readCString(language) != "en" || readCString(keywords) != "Paris\nAda" || timestamps != 1 || capacity != 32 {
						t.Error("incorrect seven-argument transcription ABI")
					}
					copy(unsafe.Slice(output, capacity), "speech\x00")
					return 17
				})
				symbols[prefix+"set_audio"] = purego.NewCallback(func(language, keywords *byte, timestamps int32) {
					audioSettings++
					if audioSettings == 1 && (readCString(language) != "en" || readCString(keywords) != "Paris\nAda" || timestamps != 1) {
						t.Error("incorrect three-argument audio options ABI")
					}
					if audioSettings == 2 && (language != nil || keywords != nil || timestamps != 0) {
						t.Error("audio options were not cleared")
					}
				})
				symbols[prefix+"embed"] = purego.NewCallback(func(input *byte, pcm *float32, samples int32, output *float32, capacity int32) int32 {
					if pcm != nil || samples != 0 {
						t.Error("text embedding received audio arguments")
					}
					return embed(input, output, capacity)
				})
				symbols[prefix+"stream_transcribe_process"] = purego.NewCallback(func(pcm *float32, samples int32, language, keywords *byte, output *byte, capacity int32) int32 {
					streams++
					if pcm == nil || samples != 2 || *pcm != 0.25 || readCString(language) != "en" || readCString(keywords) != "Paris\nAda" || capacity != 32 {
						t.Error("incorrect six-argument stream ABI")
					}
					copy(unsafe.Slice(output, capacity), "step\x00")
					return 3
				})
				symbols[prefix+"stream_transcribe_stop"] = purego.NewCallback(func(output *byte, capacity int32) int32 {
					streams++
					if capacity != 32 {
						t.Error("incorrect stream stop ABI")
					}
					copy(unsafe.Slice(output, capacity), "tail\x00")
					return 0
				})
				diagnostic := []byte("native detail\x00")
				symbols[prefix+"last_error"] = purego.NewCallback(func() *byte { return &diagnostic[0] })
			}
			resolve := func(_ uintptr, name string) (uintptr, error) {
				if address, ok := symbols[name]; ok {
					return address, nil
				}
				return 0, errors.New("symbol absent")
			}
			api, err := registerNative(42, resolve, generation)
			if err != nil {
				t.Fatal(err)
			}
			input, _ := cString("input", "hello", false)
			output := make([]byte, 32)
			if code := api.complete(&input[0], 123, output, int32(len(output))); code != 7 {
				t.Fatalf("complete code = %d", code)
			}
			if calls != 1 || gotInput != "hello" || gotTokens != 123 || gotCapacity != 32 || gotPCM != nil || gotSamples != 0 || string(output[:8]) != "response" {
				t.Fatalf("ABI arguments: calls=%d input=%q tokens=%d capacity=%d pcm=%p samples=%d output=%q", calls, gotInput, gotTokens, gotCapacity, gotPCM, gotSamples, output)
			}
			if api.handle != 42 || (api.completeAudio != nil) != modern || (api.lastError != nil) != modern || (api.streamProcess != nil) != modern || (api.streamStop != nil) != modern {
				t.Fatal("wrong native capabilities")
			}
			if modern && api.lastError() != "native detail" {
				t.Fatal("native diagnostic was not copied")
			}
			if modern {
				language, _ := cString("language", "en", false)
				keywords, _ := cString("keywords", "Paris\nAda", false)
				pcm := []float32{0.25, -0.5}
				api.setAudio(&language[0], &keywords[0], 1)
				if code := api.transcribe(&pcm[0], 2, &language[0], &keywords[0], 1, output, 32); code != 17 || string(output[:6]) != "speech" {
					t.Fatalf("transcription ABI: code=%d output=%q", code, output)
				}
				api.setAudio(nil, nil, 0)
				if transcriptions != 1 || audioSettings != 2 {
					t.Fatalf("transcriptions=%d audio settings=%d", transcriptions, audioSettings)
				}
				if code := api.streamProcess(&pcm[0], 2, &language[0], &keywords[0], output, 32); code != 3 || string(output[:4]) != "step" {
					t.Fatalf("stream ABI: code=%d output=%q", code, output)
				}
				if code := api.streamStop(output, 32); code != 0 || string(output[:4]) != "tail" || streams != 2 {
					t.Fatalf("stream stop ABI: code=%d output=%q streams=%d", code, output, streams)
				}
				options, _ := cString("options", `{"sections":["scene"]}`, false)
				if code := api.describe([]byte{9, 8, 7}, 1, 1, &options[0], &language[0], &keywords[0], output, 32); code != 5 || string(output[:6]) != "vision" || describes != 1 {
					t.Fatalf("describe ABI: code=%d output=%q describes=%d", code, output, describes)
				}
				if models := api.models(); models != 7 {
					t.Fatalf("models = %d", models)
				}
			}
			if modern {
				vector := make([]float32, 2)
				if api.embed(&input[0], nil, 0) != 2 || api.embed(&input[0], vector, 2) != 2 || embedCalls != 2 || vector[0] != 0.25 || vector[1] != -0.75 {
					t.Fatalf("embedding ABI: calls=%d output=%v", embedCalls, vector)
				}
			} else {
				if api.embed != nil || api.lastError != nil || api.transcribe != nil || api.setAudio != nil ||
					api.completeAudio != nil || api.streamProcess != nil || api.streamStop != nil || api.describe != nil || api.models != nil {
					t.Fatal("Needle 2 engine bound Cactus engine functions")
				}
				// A pre-4.0 Needle 3 library shares the four names with a different completion.
				symbols["needle_models"] = 1
				if _, err := registerNative(42, resolve, generation); !errors.Is(err, ErrUnsupportedABI) {
					t.Fatalf("Needle 3 library accepted as Needle 2: %v", err)
				}
				delete(symbols, "needle_models")
			}
			// The other generation's ABI is rejected.
			if _, err := registerNative(42, resolve, 5-generation); !errors.Is(err, ErrUnsupportedABI) {
				t.Fatalf("generation %d accepted a %s library: %v", 5-generation, prefix, err)
			}
			names := make([]string, 0, len(symbols))
			for name := range symbols {
				names = append(names, name)
			}
			for _, name := range names {
				address := symbols[name]
				symbols[name] = 0
				if _, err := registerNative(42, resolve, generation); !errors.Is(err, ErrUnsupportedABI) || !strings.Contains(err.Error(), name) {
					t.Fatalf("null %s: %v", name, err)
				}
				delete(symbols, name)
				if _, err := registerNative(42, resolve, generation); !errors.Is(err, ErrUnsupportedABI) || !strings.Contains(err.Error(), name) {
					t.Fatalf("missing %s: %v", name, err)
				}
				symbols[name] = address
			}
		})
	}
}

func TestRegisterNativeRejectsPartialABI(t *testing.T) {
	t.Parallel()
	for _, generation := range []int{2, 3} {
		for _, present := range []string{"", "needle_init", "needle_models", "cactus_init", "cactus_models"} {
			resolve := func(_ uintptr, name string) (uintptr, error) {
				if name == present {
					return 1, nil
				}
				return 0, nil
			}
			if _, err := registerNative(0, resolve, generation); !errors.Is(err, ErrUnsupportedABI) {
				t.Fatalf("generation %d with %q: %v", generation, present, err)
			}
		}
	}
}
