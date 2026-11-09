# @dropway/room-runtime

Compatibility spike for a Dropway-side [Pi](https://pi.dev/docs/latest/sdk) worker. Pi stays in this process. File, shell, and build tools are an attempt-scoped Sprite executor. This package is not mounted on the API, does not provision Sprites, and does not collect provider keys.

`EXECUTION_WIRED` is false. `services/api` room execution routes stay disabled.

## What is pinned

`@earendil-works/pi-coding-agent@1.5.0` (`VERSION` must match).

SDK behavior checked against that package:

- `createAgentSession({ noTools: "all", tools, customTools, resourceLoader, settingsManager, sessionManager, model, modelRuntime })` replaces the default tool set and skips `DefaultResourceLoader` discovery.
- `ModelRuntime.create({ credentials: InMemoryCredentialStore, modelsPath: null, allowModelNetwork: false, refreshOnCreate: false })` plus `setRuntimeApiKey` sends the gateway capability. The Anthropic client uses `model.baseUrl`, so the worker pins that to the Dropway gateway.
- `session.abort()` is how a run is cancelled. Tool `execute` receives the abort signal.
- Pi coerces a missing Anthropic `usage` object to zeros (`input_tokens || 0`). The worker records absence from the raw SSE before that happens.
- Built-in `createBashToolDefinition` is not used. Its wrapper copies `process.env` into the exec options and can write a local temp file when output is truncated.

Sprites egress follows [Sprite networking](https://docs.fly.io/sprites/concepts/networking/): empty `rules` means unrestricted, the guest policy file is read-only, and stop is not destroy. The planned policy is deny `*` plus `registry.npmjs.org` and `nodejs.org`. Model and Fly hosts are not allowed. No live DNS, raw-IP, sleep/wake, or checkpoint check is claimed.

## Tests

```sh
corepack pnpm --filter @dropway/room-runtime test
corepack pnpm --filter @dropway/room-runtime typecheck
```

Mocks cover tool calls, streaming, cancellation, usage, absent usage, denied and malformed tools, cross-attempt denial, budget exhaustion, local-tool and extension traps, clarification without a sandbox, and orphan reaping. A database store is not implemented.
