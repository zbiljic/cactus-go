//go:build (darwin && !ios) || (linux && !android) || windows

package cactus

import (
	"fmt"

	"github.com/ebitengine/purego"
)

// needle2Symbols is the needle_* ABI of the Needle 2 engine, which embeds its
// model and uses the four-argument completion.
var needle2Symbols = []string{"init", "complete", "reset", "load"}

// cactusSymbols is the cactus_* ABI that the Cactus engine exports in full.
var cactusSymbols = []string{
	"load", "models", "last_error", "init", "complete", "set_audio", "reset",
	"embed", "transcribe", "stream_transcribe_process", "stream_transcribe_stop",
	"describe",
}

// registerNative binds the ABI for a generation: needle_* for the Needle 2
// engine and cactus_* for the Cactus engine that runs Needle 3, Whistle and
// Mirage.
// A library missing any symbol of that ABI is rejected.
func registerNative(
	handle uintptr,
	symbol func(uintptr, string) (uintptr, error),
	generation int,
) (*nativeAPI, error) {
	prefix, names := "cactus_", cactusSymbols
	if generation == 2 {
		prefix, names = "needle_", needle2Symbols
	}
	engine := "Cactus engine"
	if generation == 2 {
		engine = "Needle 2 engine"
	}
	address := make(map[string]uintptr, len(names))
	for _, name := range names {
		resolved, err := symbol(handle, prefix+name)
		if err != nil || resolved == 0 {
			return nil, fmt.Errorf("%w: %s is missing %s%s", ErrUnsupportedABI, engine, prefix, name)
		}
		address[name] = resolved
	}
	if generation == 2 {
		// Needle 3 libraries before Cactus 4 also export these four names, but
		// with a six-argument needle_complete. needle_models identifies them.
		if models, _ := symbol(handle, "needle_models"); models != 0 {
			return nil, fmt.Errorf("%w: Needle 3 library is not the Needle 2 engine", ErrUnsupportedABI)
		}
	}

	api := &nativeAPI{handle: handle}
	purego.RegisterFunc(&api.init, address["init"])
	purego.RegisterFunc(&api.reset, address["reset"])
	purego.RegisterFunc(&api.load, address["load"])
	if generation == 2 {
		purego.RegisterFunc(&api.complete, address["complete"])
		return api, nil
	}
	purego.RegisterFunc(&api.completeAudio, address["complete"])
	purego.RegisterFunc(&api.lastError, address["last_error"])
	purego.RegisterFunc(&api.transcribe, address["transcribe"])
	purego.RegisterFunc(&api.setAudio, address["set_audio"])
	purego.RegisterFunc(&api.streamProcess, address["stream_transcribe_process"])
	purego.RegisterFunc(&api.streamStop, address["stream_transcribe_stop"])
	purego.RegisterFunc(&api.describe, address["describe"])
	purego.RegisterFunc(&api.models, address["models"])
	var embed func(*byte, *float32, int32, []float32, int32) int32
	purego.RegisterFunc(&embed, address["embed"])
	api.complete = func(input *byte, tokens int32, output []byte, capacity int32) int32 {
		return api.completeAudio(input, nil, 0, tokens, output, capacity)
	}
	api.embed = func(input *byte, output []float32, capacity int32) int32 {
		return embed(input, nil, 0, output, capacity)
	}
	return api, nil
}
