//go:build (darwin && !ios) || (linux && !android) || windows

package cactus

import (
	"fmt"

	"github.com/ebitengine/purego"
)

// cactusSymbols is the cactus_* ABI that every Cactus 4 engine exports in full.
// cactus_describe is bound only when vision support needs it.
var cactusSymbols = []string{
	"load", "models", "last_error", "init", "complete", "set_audio", "reset",
	"embed", "transcribe", "stream_transcribe_process", "stream_transcribe_stop",
}

// registerNative binds the cactus_* ABI of Cactus 4 engines, or the needle_*
// ABI of the Needle 2 engine and trusted older Needle 3 libraries. The exported
// symbols select the prefix and, for needle_*, the completion signature.
func registerNative(handle uintptr, symbol func(uintptr, string) (uintptr, error)) (*nativeAPI, error) {
	prefix := "needle_"
	for _, name := range cactusSymbols {
		if address, _ := symbol(handle, "cactus_"+name); address != 0 {
			prefix = "cactus_"
			break
		}
	}
	resolve := func(name string) uintptr {
		address, _ := symbol(handle, prefix+name)
		return address
	}
	if prefix == "cactus_" {
		for _, name := range cactusSymbols {
			if resolve(name) == 0 {
				return nil, fmt.Errorf("%w: missing cactus_%s", ErrUnsupportedABI, name)
			}
		}
	}

	api := &nativeAPI{handle: handle}
	models := resolve("models")
	transcribe := resolve("transcribe")
	setAudio := resolve("set_audio")
	lastError := resolve("last_error")
	embed := resolve("embed")
	streamProcess := resolve("stream_transcribe_process")
	streamStop := resolve("stream_transcribe_stop")
	audioABI := models != 0 || transcribe != 0 || setAudio != 0
	if audioABI {
		// These symbols were introduced together with the six-argument completion ABI.
		for _, capability := range []struct {
			name    string
			address uintptr
		}{
			{"models", models},
			{"transcribe", transcribe},
			{"set_audio", setAudio},
			{"last_error", lastError},
			{"embed", embed},
		} {
			if capability.address == 0 {
				return nil, fmt.Errorf("%w: missing %s%s", ErrUnsupportedABI, prefix, capability.name)
			}
		}
		// Streaming arrived with engine 3.2.0 as a pair of symbols.
		if (streamProcess != 0) != (streamStop != 0) {
			missing := "stream_transcribe_stop"
			if streamProcess == 0 {
				missing = "stream_transcribe_process"
			}
			return nil, fmt.Errorf("%w: missing %s%s", ErrUnsupportedABI, prefix, missing)
		}
	}
	if lastError != 0 {
		purego.RegisterFunc(&api.lastError, lastError)
	}
	complete := any(&api.complete)
	if audioABI {
		complete = &api.completeAudio
	}
	for _, binding := range []struct {
		name   string
		target any
	}{
		{"init", &api.init},
		{"complete", complete},
		{"reset", &api.reset},
		{"load", &api.load},
	} {
		address, err := symbol(handle, prefix+binding.name)
		if err != nil {
			return nil, fmt.Errorf("cactus: resolve %s%s: %w", prefix, binding.name, err)
		}
		if address == 0 {
			return nil, fmt.Errorf("cactus: resolve %s%s: null symbol", prefix, binding.name)
		}
		purego.RegisterFunc(binding.target, address)
	}
	if audioABI {
		purego.RegisterFunc(&api.transcribe, transcribe)
		purego.RegisterFunc(&api.setAudio, setAudio)
		if streamProcess != 0 {
			purego.RegisterFunc(&api.streamProcess, streamProcess)
			purego.RegisterFunc(&api.streamStop, streamStop)
		}
		api.complete = func(input *byte, tokens int32, output []byte, capacity int32) int32 {
			return api.completeAudio(input, nil, 0, tokens, output, capacity)
		}
	}
	if embed != 0 {
		if audioABI {
			var embedAudio func(*byte, *float32, int32, []float32, int32) int32
			purego.RegisterFunc(&embedAudio, embed)
			api.embed = func(input *byte, output []float32, capacity int32) int32 {
				return embedAudio(input, nil, 0, output, capacity)
			}
		} else {
			purego.RegisterFunc(&api.embed, embed)
		}
	}
	return api, nil
}
