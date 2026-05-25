# Cerberus integration notes

Foundry delegates agent execution to the `cerberus` CLI.

## Commands used

For workflow phases Foundry runs, from the target project repo:

```sh
cerberus start --name foundry-w<workflow>-p<phase> --prompt-file <tmpfile> [--image ...] [--model ...] [--profile-file ...] [--callback ... --output jsonl]
cerberus review --name foundry-w<workflow>-p<phase> --diff
cerberus review --name foundry-w<workflow>-p<phase>
```

The workflow runner does not call `cerberus clean` before `cerberus start`; phase names must therefore be suitable for reuse or cleanup must happen outside the workflow start path.

If Cerberus produced a commit, Foundry cherry-picks `cerberus/foundry-w<workflow>-p<phase>` into the target repository.

For spec builder Foundry uses `cerberus chat`, `cerberus message`, `cerberus close`, and `cerberus clean` with working directory set to the private memory repo.

## Config mapping

- `cerberus_bin`: executable path.
- `cerberus_image`: adds `--image` when non-empty.
- `cerberus_model`: adds `--model` when non-empty.
- `cerberus_profile`: names a Foundry profile. Foundry writes a temporary JSON profile file and passes `--profile-file`.

Profiles are managed in Settings and can include default model, default image, AWS profile/region, and extra environment JSON.

## Events

Foundry passes a callback URL to Cerberus. The callback endpoint accepts compact JSON events at:

```text
/api/cerberus/events
```

Foundry stores/publishes text deltas, turn completions, and `update_spec` tool-use events. High-volume raw/tool-result/log events are dropped.

## Prompt context

Workflow prompts include:

1. target repository root guidance,
2. approved project memory, when configured and present,
3. spec global context,
4. the phase goal,
5. the PoC or Polish track overlay.
