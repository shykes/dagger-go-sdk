# workspace-module-source

Temporary helper for `Mod.generate`.

It runs `Workspace.directory(...).asModuleSource(...)` from a nested Dagger
client instead of from the Dang module client. This avoids
https://github.com/dagger/dagger/issues/13139, where `DIR_SOURCE` local
dependency resolution loads user defaults with the module client context and
then fails trying to use `Host.findUp(".env")`.

When core gives `DIR_SOURCE` a valid caller/workspace context for user-default
loading, delete this helper and restore the direct Dang generate path:

```dang
GoSdk().workspaceModuleSource(ws, path).generatedContextChangeset
```
