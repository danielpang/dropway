# @dropway/room-runtime

Hosted integration for a Dropway-side [Pi](https://pi.dev/docs/latest/sdk) worker. Pi stays in this process. File, shell, and build tools are an attempt-scoped Sprite executor. The daemon claims API jobs, provisions disposable Sprites, stages uploaded assets and returns immutable candidates. Personal provider keys remain encrypted in the API; this worker receives only scoped capabilities.

`EXECUTION_WIRED` is true; API, dashboard and worker feature flags remain default-off. See [development setup](../../docs/hosted-collaboration-development.md) and [live security gate](scripts/SPRITES-VERIFICATION.md).

## What is pinned

`@earendil-works/pi-coding-agent@1.5.0` (`VERSION` must match).

SDK behavior checked against that package:

- `createAgentSession({ noTools: "all", tools, customTools, resourceLoader, settingsManager, sessionManager, model, modelRuntime })` replaces the default tool set and skips `DefaultResourceLoader` discovery.
- `ModelRuntime.create({ credentials: InMemoryCredentialStore, modelsPath: null, allowModelNetwork: false, refreshOnCreate: false })` plus `setRuntimeApiKey` sends the gateway capability. The Anthropic client uses `model.baseUrl`, so the worker pins that to the Dropway gateway.
- `session.abort()` is how a run is cancelled. Tool `execute` receives the abort signal.
- Pi coerces a missing Anthropic `usage` object to zeros (`input_tokens || 0`). The worker records absence from the raw SSE before that happens.
- Built-in `createBashToolDefinition` is not used. Its wrapper copies `process.env` into the exec options and can write a local temp file when output is truncated.

Sprites egress follows [Sprite networking](https://docs.fly.io/sprites/concepts/networking/): empty `rules` means unrestricted, the guest policy file is read-only, and stop is not destroy. The hosted static-site policy denies all network access. A separate explicit build policy exists but is not enabled by this daemon. Model and Fly hosts are not allowed. No live DNS, raw-IP, sleep/wake, or checkpoint check is claimed.

## Tests

```sh
corepack pnpm --filter @dropway/room-runtime test
corepack pnpm --filter @dropway/room-runtime typecheck
```

Mocks cover tool calls, streaming, cancellation, usage, absent usage, denied and malformed tools, cross-attempt denial, budget exhaustion, local-tool and extension traps, clarification without a sandbox, and orphan reaping. API integration tests use real local Postgres and fake model-provider responses. Runtime tests exercise actual Pi orchestration against fake API/Sprite boundaries. Live provider and Sprite checks remain for development acceptance.
