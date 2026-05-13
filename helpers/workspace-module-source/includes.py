import json
import posixpath
import sys

seen = set()
include = set()


def clean(path):
    path = (path or ".").strip("/") or "."
    path = posixpath.normpath(path)
    if path == ".":
        return "."
    if path == ".." or path.startswith("../"):
        raise SystemExit("path escapes workspace: " + path)
    return path


def local_source(source):
    if not source:
        return None
    if "://" in source or source.startswith("git@") or source.startswith("github.com/"):
        return None
    if "@" in source:
        return None
    return source


def join(base, path):
    if not path:
        return clean(base)
    if path.startswith("/"):
        return clean(path)
    if base == ".":
        return clean(path)
    return clean(posixpath.join(base, path))


def add_path(base, path):
    negated = path.startswith("!")
    if negated:
        path = path[1:]
    path = join(base, path)
    include.add(("!" if negated else "") + ("**" if path == "." else path))


def add_dir(base, path):
    path = join(base, path)
    include.add("**" if path == "." else path + "/**")


def add_module(path):
    path = clean(path)
    if path in seen:
        return
    seen.add(path)

    config_path = "dagger.json" if path == "." else path + "/dagger.json"
    try:
        with open("/ws/" + config_path) as f:
            config = json.load(f)
    except FileNotFoundError:
        raise SystemExit("missing dagger.json: " + config_path)

    include.add(config_path)
    add_dir(path, config.get("source") or ".")

    values = config.get("include") or []
    if isinstance(values, str):
        values = [values]
    for value in values:
        add_path(path, value)

    for key in ("dependencies", "toolchains"):
        for dep in config.get(key) or []:
            source = dep.get("source") if isinstance(dep, dict) else dep
            source = local_source(source)
            if source:
                add_module(join(path, source))

    blueprint = config.get("blueprint")
    source = blueprint.get("source") if isinstance(blueprint, dict) else blueprint
    source = local_source(source)
    if source:
        add_module(join(path, source))


add_module(sys.argv[1])
print("\n".join(sorted(include)))
