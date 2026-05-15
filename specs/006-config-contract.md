# 006: Config Contract — Schema as Source of Truth

## Problem

Foundry's config is currently a flat YAML file parsed into a Go struct
(`internal/config/config.go`). This works, but as config grows (reviewer
model settings, per-project defaults, track rule overrides, future webhook
config), the risks are:

- Keys get added to the struct but not documented.
- Old keys get removed from the struct but users still have them in config.yaml.
  Silent ignore vs. hard error is decided ad-hoc.
- Defaults are scattered between struct tags, code, and config.yaml examples.
- No way to validate config without starting the server.

## What openclaw does

OpenClaw treats the config schema as the single source of truth:

1. **Zod schema** defines every key, its type, default, and help text.
2. **JSON Schema** is derived from Zod — used for editor autocomplete and
   external validation.
3. **Retired keys** are explicitly tracked. Validation rejects them with an
   error, not a warning. Doctor migrates them.
4. **Baseline hash** is generated and tracked in git. CI fails if the schema
   changes without regenerating docs.
5. **Load-time validation** uses the schema. Doctor-time validation handles
   legacy compat.

## What foundry should do

### Go struct stays the source of truth

Foundry is Go, not TypeScript. There's no Zod. The Go config struct should be
the schema, with validation built around it rather than beside it.

### 1. Strict YAML parsing with DisallowUnknownFields

```go
decoder := yaml.NewDecoder(f)
decoder.KnownFields(true)  // yaml.v3 — rejects unknown keys
```

If config.yaml has a key the struct doesn't know about, startup fails with:

```
config error: unknown key 'cerberus_timeout' in config.yaml
hint: this key was renamed to 'default_phase_timeout_seconds' in v0.0.2
hint: run 'foundry doctor --fix' to migrate
```

### 2. Retired keys list

```go
var retiredKeys = map[string]string{
    "cerberus_timeout": "renamed to default_phase_timeout_seconds in v0.0.2",
    "review_model":     "moved to reviewer.model in v0.0.3",
}
```

When an unknown key is detected, check if it's retired. If so, include the
migration hint. Doctor handles the actual rename.

### 3. Defaults in one place

All defaults live on the struct as either field tags or a `DefaultConfig()`
function. Not in config.yaml examples, not in code that reads config values:

```go
func DefaultConfig() Config {
    return Config{
        ServerPort:                 8080,
        MaxConcurrentWorkflows:     1,
        DefaultWorkflowBudgetUSD:   5.00,
        DefaultPhaseTimeoutSeconds: 1800,
        Reviewer: ReviewerConfig{
            Model: "claude-haiku-4-5",
        },
    }
}
```

### 4. Validate without starting

```
foundry config validate           # parse + validate config.yaml
foundry config show               # show resolved config with defaults applied
foundry config show --defaults    # show only default values
```

This is a subcommand, not a startup step. Useful in CI and for debugging.

### 5. Config documentation generation (later)

When config grows past ~15 keys, generate a markdown table from struct field
tags:

```go
type Config struct {
    ServerPort int `yaml:"server_port" help:"HTTP port for the foundry API" default:"8080"`
}
```

Script reads struct tags, outputs markdown. If the output changes, commit it.
Not needed for v0.0.1 — the struct is small enough to document manually.

## Build order

1. Switch to `KnownFields(true)` in config loader. Handle the error with a
   useful message.
2. Add `retiredKeys` map. Check on unknown-key error.
3. Add `DefaultConfig()` function. Remove scattered defaults.
4. Add `foundry config validate` subcommand.
5. Wire unknown-key errors to "run foundry doctor" hints.

## What this does NOT do

- Does not add JSON Schema generation. Go struct is enough for now.
- Does not add editor autocomplete. YAML Language Server can infer from
  the example config.
- Does not add config profiles or per-project overrides. One config.yaml.
- Does not add env var overrides. Config.yaml is the only input.
