# Environment suites

Six acceptance suites for `cactusz eval`, one per directory: `tools.json`,
`system.txt` and `cases.jsonl`.

```sh
go run ./cmd/cactusz eval --threads 4 \
  --tools cmd/cactusz/testdata/environments/smart_home/tools.json \
  --system-file cmd/cactusz/testdata/environments/smart_home/system.txt \
  --cases cmd/cactusz/testdata/environments/smart_home/cases.jsonl
```

On the pinned engine every suite fails one or two critical cases, so the
command exits with status 1. Upstream counts a suite as passed at 90% of its
cases with no critical failure; add `--min-score 0.9` for the score half of
that bar.

`baseline.json` lists the failing cases of each suite, by line number, on the
pinned engine with 4 threads, as in the command above; the thread count changes
the model's output.
With `CACTUS_TEST_NATIVE=1`, `TestNativeEnvironmentSuites` fails when any case
moves in either direction. After an engine upgrade, check the changed cases
and update the baseline.

## Source

The tools, system prompts and cases come from the `cactus.environments`
package of [Cactus](https://github.com/cactus-compute/cactus) at commit
`bb6ee020e6139b1fbd782fb6d7a9ecca4c80025b`, licensed under the Apache License
2.0 in [LICENSE](LICENSE). Changes: the Python tool definitions are exported as
the JSON schemas the package sends to the engine, in their key order; each
case's `query` and `calls` became `input` and `want`; and each system prompt
starts with a fixed date fact, as the package adds the current one.
