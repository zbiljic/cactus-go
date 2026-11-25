# Speech

`NewWhistle` loads the pinned ~16.9 MB `whistle.cact` archive from
[Cactus-Compute/whistle](https://huggingface.co/Cactus-Compute/whistle) and runs on
the Cactus engine. Speech weights are fetched only when requested; standalone
transcription does not require text weights.

## Transcription

```go
speech, err := cactus.NewWhistle(ctx, cactus.WhistleConfig{})
if err != nil {
	log.Fatal(err)
}
transcript, err := speech.Transcribe(ctx, pcm, cactus.AudioOptions{
	Language:       "en",
	Keywords:       []string{"Paris", "Ada"},
	WordTimestamps: true,
})
```

Supply mono `[]float32` PCM at 16000 Hz, with finite samples in `[-1, 1]`, of
any length: up to 30 seconds runs as one pass, and longer audio runs as
consecutive 30 second windows with word times counted from the start of the
clip. The output buffer grows for long clips. Empty clips and silence return
empty text and language. `Language` is empty for detection, or one of
`Languages()`, which reads the language tokens of the loaded weights: `en`,
`de`, `fr`, `es`, `it`, `nl` and `pl` for the published model, or the languages
of a fine-tuned model. Transcripts
include `TTFTMS`, `DecodeTPS`, and optional words with start/end times in seconds
and probabilities. File decoding, microphone capture and resampling belong to
the caller; the [PCM example](../examples/transcribe) accepts little-endian float32
samples.

## Live transcription

`Stream` transcribes as audio arrives. Feed about one second of PCM per
`Process` call; each step returns the words two consecutive passes agreed on,
with times from the start of the stream, plus the unconfirmed `Pending` tail.
`Stop` commits the tail and ends the stream.

```go
live, err := speech.Stream(cactus.AudioOptions{Language: "en"})
if err != nil {
	log.Fatal(err)
}
for chunk := range chunks {
	step, err := live.Process(ctx, chunk)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(step.Text, " ")
}
tail, err := live.Stop(ctx)
```

The engine holds one stream per process. While a stream is open, other
speech calls, a second stream, and loading different speech weights return
`ErrStreamActive`; a failed `Process` ends the stream, and `Close` on its
Whistle ends it too. Pass `-stream` to the [PCM example](../examples/transcribe)
to see it on a file.

## Audio tool calling

To transcribe and produce tool calls in one native turn, pass a Needle 3 agent
created by `New`:

```go
response, err := speech.Complete(ctx, agent, pcm, cactus.AudioOptions{},
	cactus.DefaultMaxNewTokens)
```

The response includes optional `AudioText`, `AudioLanguage`, `AudioWords`,
`AudioTTFTMS` and `AudioDecodeTPS` fields. This is a raw completion that flags
dates against the years heard, as `Complete` does with written years: validate
it with `ValidateResponse`, passing `AudioText` as a source, before acting on
calls, and never execute suppressed calls automatically. Stateless agents reset
once before the audio request; stateful agents retain context for manual
tool-result continuations.

## Speech features

`Embed` returns the Whistle encoder output for up to 30 seconds of PCM, for
speaker or sound similarity, clustering or a downstream classifier:

```go
features, err := speech.Embed(ctx, pcm)
```

The result is flattened rows of 512 floats for the published model, so
`len(features)/512` is the row count. There is about one row per 80 ms of audio
(1280 samples); a trailing partial frame adds a row only once it holds a full
10 ms hop (160 samples), and any nonempty clip returns at least one row. Longer
clips fail with a `NativeError`; split them first. Empty audio is
rejected, and an open stream returns `ErrStreamActive`.

## Runtime and custom weights

Whistle shares one serialized Cactus engine runtime and library path with the
other models the engine runs, such as Needle 3 text.
Loading or switching speech models preserves the active text conversation;
switching text agents follows the existing reset rules. Audio options are
cleared after each completion. An in-progress native call cannot be interrupted
by context cancellation.

`WhistleConfig.WeightsPath` selects a trusted custom speech archive;
`LibraryPath` and `CacheDir` work like `Config`. The shared `.cact` format tag
alone does not distinguish speech from text. Both loading paths check the
recognized manifest layout and reject wrong-kind or unknown-kind archives
before loading them. The current speech classifier accepts the published
version-1 audio manifest; a future format requires a library update.

## Offline setup

`FetchWhistle(ctx, cactus.FetchOptions{CacheDir: cacheDir})` prepares the engine
and speech weights for offline use, without fetching text weights. `Platform`
selects the target desktop platform; `Generation` must be zero or 3. Preserve the
`.sha256` markers and use the same cache directory with `NewWhistle`. For audio
tool calling, also prepare text weights with `FetchEngine`.
