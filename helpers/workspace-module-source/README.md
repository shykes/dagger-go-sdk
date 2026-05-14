# workspace-module-source

Temporary helper for `init`, `Mod.generate`, and `Mod.deps`.

It runs `Workspace.directory(...).asModuleSource(...)` from a nested Dagger
client instead of from the Dang module client. This avoids
https://github.com/dagger/dagger/issues/13139, where `DIR_SOURCE` local
dependency resolution loads user defaults with the module client context and
then fails trying to use `Host.findUp(".env")`.

The helper exports the generated before/after directories rooted at the
caller's `Workspace.path`, not always at the target module root. Returned
changesets are applied by the CLI relative to that caller path, so this keeps
paths correct whether the caller runs from the module directory or from a parent
workspace.

For `init`, the helper builds an add-only diff itself: empty before directory,
generated module context inserted at the module path relative to
`Workspace.path`. That avoids both the module-context local source timeout and
the module-rooted changeset shape.

When core gives `DIR_SOURCE` a valid caller/workspace context for user-default
loading, delete this helper and restore the direct Dang generate path:

```dang
GoSdk().workspaceModuleSource(ws, path).generatedContextChangeset
```
