# Helper Cleanup Handoff

This document records the intended helper boundary, the cleanup implemented so
far, and the remaining core/Dang gaps that prevent deleting the helper operation
subcommands entirely.

## Goal

Keep the helper responsible for the one thing the current module cannot do
directly: load a `ModuleSource` from a `Directory`.

That is the hard boundary. The caller module function can prepare object IDs,
paths, and flags, but `Directory -> ModuleSource` must happen outside the module
function evaluator in a plain helper process with a nested Dagger client.

The ideal boundary is:

```sh
WORKSPACE_ID=... module-source REF [--cwd CWD] [--local] [--name NAME]
```

The helper should print exactly one `ModuleSourceID`. Dang should then recover a
`ModuleSource` handle from that ID and own generation, dependency mutation, and
changeset rooting. This would not violate the hard boundary because the
`Directory -> ModuleSource` load already happened in the helper.

## Why A Helper Exists

Module functions cannot safely perform `Directory.asModuleSource(...)` for
caller workspace directories. Direct calls from this Dang module can lose the
original caller workspace context during local dependency and user-default
resolution. One observed failure path tries to load user defaults from the
module client context and then calls `Host.findUp(".env")` without the caller
host cwd/session.

The helper opens a nested Dagger client from inside the workspace operation, so
workspace-backed local refs can be resolved with the caller workspace object
instead of the helper container filesystem.

Do not turn this helper into a Dagger/Dang module function. That puts the
forbidden `Directory -> ModuleSource` conversion back inside the module function
boundary.

## Preserve

- Go SDK module generation works for workspace modules with local dependencies.
- `init --name foo` works in an empty repo.
- `init --name foo --path ./foo/bar` works in an empty repo.
- Dependency add/remove/update returns changesets rooted at `Workspace.path`.
- Local dependency refs resolve relative to the target module.
- Remote and ambiguous non-local refs flow through core `moduleSource(ref)`.
- The helper does not own policy that core can already handle.

## Implemented Now

The helper binary is built as `module-source`. The source directory is still
`helpers/workspace-module-source` to keep the patch narrow.

Environment:

- `WORKSPACE_ID`: required for workspace-backed resolution.
- `SEED_DIRECTORY_ID`: optional synthetic seed directory for init generation.

Primary command:

```sh
module-source REF [--cwd CWD] [--local] [--name NAME]
```

This implements the desired ID-only protocol and prints a `ModuleSourceID`.

Temporary operation commands:

```sh
module-source generate REF CHANGESET_ROOT BEFORE_DIR AFTER_DIR [--cwd CWD] [--local] [--name NAME]
module-source deps-add TARGET_REF DEP_REF CHANGESET_ROOT BEFORE_DIR AFTER_DIR [--name NAME]
module-source deps-remove TARGET_REF DEP_NAME CHANGESET_ROOT BEFORE_DIR AFTER_DIR
module-source deps-update TARGET_REF DEP_NAME CHANGESET_ROOT BEFORE_DIR AFTER_DIR
module-source render-template MODULE_NAME TEMPLATE_DIR OUT_DIR
```

Dang still calls these operation commands for now. They are intentionally thin:
the helper resolves the source, invokes the relevant core `ModuleSource` method,
and exports before/after directories for Dang to turn into a changeset.

## Current Blockers To The Ideal Boundary

1. Dang cannot currently recover a `ModuleSource` handle from the raw ID printed
   by the helper.

   Attempting `loadModuleSourceFromID(helperStdout)` fails with a type mismatch:
   the raw string, even with a `ModuleSourceID` type hint, is not the
   `ModuleSource` handle Dang expects. `vito/dang#46` documents the related
   GraphQL object-list projection issue; it does not provide a way to cast an
   arbitrary stdout string into a GraphQL object handle.

   Once Dang has a supported way to consume a helper-created `ModuleSourceID` as
   a `ModuleSource` handle, generation and dependency mutation can move out of
   the helper while keeping `Directory -> ModuleSource` in the helper.

2. Local dependency mutation currently rejects workspace-backed `DIR_SOURCE`
   values.

   Using a workspace-backed dependency source with `withDependencies` hits
   `unhandled module source kind: DIR_SOURCE`. For dependency commands only,
   Dang mounts the workspace at `/ws` and the helper uses
   `client.ModuleSource("/ws/...", RequireKind: LOCAL_SOURCE)` for local target
   and dependency sources. This is a compatibility bridge, not the desired
   final boundary.

3. Init generation from a seeded directory still needs the nested client.

   Init seeds a `Directory`, then needs that directory loaded as a
   `ModuleSource`. That is the same hard boundary. The helper now receives
   `SEED_DIRECTORY_ID` instead of a mounted seed directory, so it avoids the
   previous empty-result mount failure while keeping init generation in the
   nested client.

## Resolution Rules

The helper only positively identifies workspace-local refs. Everything else
falls through to core parsing unless the caller explicitly requires a local ref.

1. Load `Workspace` from `WORKSPACE_ID`.
2. Determine cwd:
   - `--cwd`, when provided.
   - otherwise `Workspace.path`.
3. Resolve the candidate workspace path:
   - absolute refs are cleaned inside the workspace boundary.
   - relative refs are joined with cwd.
4. Check whether the candidate exists as a workspace directory.
5. If it exists, compute the include patterns for that module and its local
   dependency tree, then build:

```go
src := ws.
    Directory("/", dagger.WorkspaceDirectoryOpts{Include: include}).
    AsModuleSource(dagger.DirectoryAsModuleSourceOpts{
        SourceRootPath: candidate,
    })
```

6. If it does not exist:
   - `--local` returns an error.
   - otherwise use core parsing:

```go
src := client.ModuleSource(ref, dagger.ModuleSourceOpts{
    DisableFindUp: true,
})
```

7. If `--name` is set, apply `src = src.WithName(name)`.
8. Print `src.ID(ctx)`.

## Dang Integration Today

Init:

- If no path is provided and no `.dagger` directory exists, use
  `.dagger/modules/<name>`.
- Normalize explicit paths like `./foo/bar` to `foo/bar`.
- Build a seed directory in Dang.
- Pass the seed by object ID:

```dang
.withEnvVariable("SEED_DIRECTORY_ID", toJSON(seeded.id))
.withExec(["module-source", "generate", "--local", moduleChangesetPath, ".", "/before", "/after"])
```

Module generation:

```dang
.withExec(["module-source", "generate", "--cwd", ".", "--local", path, workspacePath, "/before", "/after"])
```

Dependency add/remove/update:

- Build the helper with `WORKSPACE_ID`.
- Mount the caller workspace at `/ws`, excluding `.git`.
- Add a minimal `/ws/.git/HEAD`.
- Call the thin dependency subcommand.

This `/ws` mount should disappear once core accepts workspace-backed
`DIR_SOURCE` dependency sources.

## Desired Final Dang Integration

When helper-created `ModuleSourceID` values can be consumed as `ModuleSource`
handles, replace operation commands with:

```dang
let src = loadModuleSourceFromID(id: helperStdout)
let changes = src.generatedContextChangeset
changes.after.directory(ws.path).changes(changes.before.directory(ws.path))
```

Dependency add should become:

```dang
let target = loadModuleSourceFromID(id: helperSourceID(ws, path, local: true))
let dep = loadModuleSourceFromID(id: helperSourceID(ws, source, cwd: path, name: name))
let changes = target.withDependencies([dep]).generatedContextChangeset
changes.after.directory(ws.path).changes(changes.before.directory(ws.path))
```

Remove/update are the same shape: load the target source ID, call the core
mutation, and root the returned changeset in Dang.

## Follow-Up Core/Dang Issue

Request a first-class workspace-scoped module source API so this module does not
need to shell out to a helper at all:

```graphql
Workspace.moduleSource(refString: String!, cwd: String, name: String): ModuleSource!
```

The API should:

- resolve workspace-local refs from the caller workspace context.
- delegate remote and ambiguous refs to core module source parsing.
- preserve caller context for local dependency and user-default resolution.
- return a real `ModuleSource` handle that Dang can mutate directly.

## Verification Checklist

Helper tests:

```sh
cd helpers/workspace-module-source
go test ./...
```

Function surface:

```sh
dagger functions
```

Empty repo init:

```sh
dagger -m /path/to/go-sdk call init --name foo added-paths
```

Empty repo init with explicit nested path:

```sh
dagger -m /path/to/go-sdk call init --name foo --path ./foo/bar added-paths
```

Module generation smoke:

```sh
dagger -m /path/to/go-sdk call mod --path . generate added-paths
```

Local dependency add smoke:

```sh
dagger -m /path/to/go-sdk call mod --path app deps add --source ../lib --name lib modified-paths
```
