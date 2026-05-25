# Private memory repo setup

Foundry 0.9 uses a separate private git repository as durable project memory. Configure it with `memory_repo_path` in `config.yaml`.

## Create the repo

```sh
mkdir -p ~/git/foundry-memory
cd ~/git/foundry-memory
git init
git config user.name "Foundry"
git config user.email "foundry@example.invalid"
```

Set:

```yaml
memory_repo_path: "~/git/foundry-memory"
```

Restart Foundry after changing config.

## Namespaces

Each Foundry project has a `memory_namespace`. On create, blank defaults to the project name. The namespace maps to a directory inside the memory repo:

```text
<memory_repo_path>/<memory_namespace>/
```

Only non-hidden `.md` files under that namespace are loaded as approved memory. Hidden files/directories, `.git`, and symlinked namespace paths are ignored or rejected.

Example:

```text
foundry-memory/
  my-service/
    intent/README.md
    intent/Principles.md
    notes.md
```

Approved memory is prepended to workflow phase prompts and spec-builder prompts.

## Memory update review

After a workflow, the workflow page can create a memory update proposal. Reviewers can accept, reject, or revise it.

Accepting writes:

```text
<memory_repo_path>/<memory_namespace>/workflow-updates/workflow-<id>.md
```

and runs:

```sh
git -C <memory_repo_path> add -- <file>
git -C <memory_repo_path> commit -m "Accept memory update for workflow <id>" -- <file>
```

Make sure the memory repo has git identity configured and no conflicting index/working-tree state before accepting updates.

## Spec builder

Spec builder sessions run Cerberus with working directory set to `memory_repo_path`. When a draft is saved for a project, Foundry also writes the final spec markdown under:

```text
<memory_repo_path>/<memory_namespace>/specs/draft-<id>-<title>.md
```
