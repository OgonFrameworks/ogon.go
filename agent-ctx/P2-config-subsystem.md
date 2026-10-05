# P2 — Config subsystem

Task ID: P2
Agent: Config subsystem
Scope: `config/` package only.

## Files written

- `config/config.go` — Loader, Config, Schema, Source, UnknownKey, Explanation, fileSystem, LoaderOpt funcs, Load()
- `config/sources.go` — DefaultSchema, parseYAML, flatten, parseDotEnv, buildEffectiveEnv, applyDefaults/YAMLLayer/FlatLayer/EnvOverrides, resolveSecret, detectUnknownKeys, suggest, levenshtein
- `config/interpolate.go` — Interpolate() + interpolateValue() (recursive over maps/slices)
- `config/env.go` — OGONPrefix, ParseEnvName, ParseEnvNameWithSchema (primary + secondary candidate)
- `config/explain.go` — Explain(key) + Explanation.String() (redacts URL creds)
- `config/redact.go` — Redact() + RedactValue() (regex over scheme://user:pass@)
- `config/config_test.go` — 35 tests, all pass

## Deps added via `go get`

- `github.com/goccy/go-yaml@v1.19.2` (now in go.mod require, was indirect; promoted by import)

## Verification

- `go build ./config/...` → clean
- `go test ./config/...` → PASS (35 tests, 0.005s)
- `go vet ./config/...` → clean
- `gofmt -l config/` → empty

## Key design decisions

1. **Layer model**: every source produces a flat `map[string]any` (dotted keys). The Loader applies them in precedence order, mutating `resolved[]` and appending to `trace[key]` per layer.
2. **Effective env**: real env (or `WithEnvMap`) is augmented by `.env` *without overriding existing entries*. The merged map feeds both `${VAR}` interpolation in YAML layers *and* `OGON_*` override mapping.
3. **Env mapping** (`env.go`): primary candidate replaces only the first `_` with `.` (preserving underscores like `read_timeout`); secondary candidate replaces all `_` with `.` (handles nested `db.pool.max`). Schema-known candidate wins.
4. **file:// secrets**: resolved at YAML-apply time; the resolved content lives in `resolved[]` (used by `Get`) but the trace records `"file://<resolved>"` so `Explain` never reveals secret content.
5. **Redaction**: applied only at the rendering boundary (`Explain.String`, `Redact`, `RedactValue`); raw values in `resolved[]` are untouched so consumers (e.g. `db.Open`) see real credentials.
6. **Unknown keys**: every layer's flat keys are checked against `schema.Known`; nearest known key within Levenshtein ≤ 2 is the typo suggestion. Strict mode returns `diag.CodeConfigUnknownKey` (`OGON-K0003`).
7. **Diag integration**: parse failures → `OGON-K0001`; unknown keys in strict → `OGON-K0003`. Reuses `diag.Diag` from Phase 1.
8. **FileSystem interface** (`memFS` for tests, `osFS` for prod): keeps tests hermetic and lets `file://` reads be tested without real disk I/O for the YAML layer.

## Public API surface (for downstream phases)

- `NewLoader(opts...) *Loader`
- `LoaderOpt`: `WithYAMLFile`, `WithYAMLBytes`, `WithEnvYAML`, `WithEnvYAMLFile`, `WithEnvYAMLBytes`, `WithDotEnv`, `WithDotEnvBytes`, `WithEnv`, `WithEnvMap`, `WithExplicit`, `WithFlags`, `WithStrict`, `WithSchema`, `WithLogger`, `WithFileSystem`
- `(*Loader).Load(ctx) (*Config, error)`
- `(*Config).Get(key) any`
- `(*Config).GetString(key) string`
- `(*Config).Explain(key) Explanation`
- `(*Config).UnknownKeys() []UnknownKey`
- `Interpolate(s, env) string`
- `ParseEnvName(envName) string`
- `Redact(s) string` / `RedactValue(v) any`

## Hooks for ogon.Boot (Phase 6 integration, not yet wired)

`ogon.Boot(BootOpts)` should call (sketch):
```go
cfg, err := config.NewLoader(
    config.WithYAMLFile(orEmpty(opts.ConfigPath, "ogon.yaml")),
    config.WithEnvYAML(opts.Env),
    config.WithDotEnv(".env"),
    config.WithEnv(),
).Load(ctx)
```
Explicit overrides from `BootOpts` map to `WithExplicit`; CLI flag parsing (in `cli/`) maps to `WithFlags`.

## Known limits / next-phase work

- No typed struct binding (CFG mentions it as a rule, not a deliverable; left for record/ subsystems that own schema-per-section).
- Levenshtein is O(n*m); fine for the ~21-key default schema. If a downstream schema grows huge, swap to BK-tree.
- External secret managers (CFG-012, CFG-017) ship as modules — not in scope for core config.
