# module-source

Temporary nested-client helper for workspace-aware module source operations.

The root constraint is that this Dang module's functions cannot safely load a
`ModuleSource` from a `Directory`. That `Directory -> ModuleSource` conversion
must stay outside the module function evaluator, in this helper process with its
own nested Dagger client.

The intended narrow boundary is:

```sh
WORKSPACE_ID=... module-source REF [--cwd CWD] [--local] [--name NAME]
```

That command behaves like core `moduleSource(refString:)`, except refs that
positively resolve to directories in the caller workspace are loaded with
`Workspace.directory(...).asModuleSource(...)`. It prints exactly one
`ModuleSourceID` to stdout; diagnostics go to stderr.

This ID-only boundary is implemented in the helper, but the caller currently
cannot use the printed ID to recover a `ModuleSource` handle in Dang. A direct
`loadModuleSourceFromID(id: stdout)` still fails because a plain string, even
with a `ModuleSourceID` hint, is not the GraphQL object handle Dang expects.
Until there is a supported object-ID handoff, this helper also keeps thin
operation subcommands that run in the same nested-client context and export
before/after directories for Dang to turn back into a changeset.

## Environment

- `WORKSPACE_ID`: required for workspace-backed source resolution.
- `SEED_DIRECTORY_ID`: optional. When present, `generate` uses that directory as
  a synthetic source and does not read from the workspace.

Both env vars may be raw object IDs or JSON-encoded strings.

## Commands

```sh
module-source REF [--cwd CWD] [--local] [--name NAME]
```

Prints a `ModuleSourceID`. This is the target protocol for future Dang
integration.

```sh
module-source generate REF CHANGESET_ROOT BEFORE_DIR AFTER_DIR [--cwd CWD] [--local] [--name NAME]
```

Generates a module source and exports `changes.before.directory(CHANGESET_ROOT)`
and `changes.after.directory(CHANGESET_ROOT)` to the given local paths.

When `SEED_DIRECTORY_ID` is set, `REF` is the seed source root path and the
before directory is created directly on the helper filesystem instead of
exporting an empty Dagger directory. This avoids mounting the seed into the
helper container and fixes the empty-result mount failure seen by `init --path`.

```sh
module-source deps-add TARGET_REF DEP_REF CHANGESET_ROOT BEFORE_DIR AFTER_DIR [--name NAME]
module-source deps-remove TARGET_REF DEP_NAME CHANGESET_ROOT BEFORE_DIR AFTER_DIR
module-source deps-update TARGET_REF DEP_NAME CHANGESET_ROOT BEFORE_DIR AFTER_DIR
```

These remain because `ModuleSource.withDependencies` currently rejects
workspace-backed `DIR_SOURCE` local dependencies with `unhandled module source
kind: DIR_SOURCE`. The Dang caller mounts the workspace at `/ws` only for these
dependency commands so local deps can use core local-source semantics.

```sh
module-source render-template MODULE_NAME TEMPLATE_DIR OUT_DIR
```

Legacy init template renderer. It stays here because it shares the small Go
template support code.

## Removal Path

Delete the operation subcommands once there is a supported way for a module
function to consume a helper-created `ModuleSourceID` as a `ModuleSource` handle,
without moving the `Directory -> ModuleSource` load back into the module
function. Core also needs to accept workspace-backed `DIR_SOURCE` values for
dependency mutation. At that point Dang can call the ID command, load the
source, and own generation, dependency mutation, and changeset rooting directly.
