import json
import os
import posixpath
import sys

CONFIG_ROOT = os.environ.get("DAGGER_CONFIG_ROOT", "/dagger-configs")


def clean(path):
    path = posixpath.normpath(path.strip("/"))
    if path in ("", "."):
        return "."
    return path


def join(base, ref):
    if ref.startswith("/"):
        return clean(ref)
    if base == ".":
        return clean(ref)
    return clean(posixpath.join(base, ref))


def dependency_sources(config_path):
    try:
        with open(posixpath.join(CONFIG_ROOT, config_path)) as f:
            config = json.load(f)
    except FileNotFoundError:
        return []

    deps = []
    for dep in config.get("dependencies") or []:
        if isinstance(dep, str):
            deps.append(dep)
        elif isinstance(dep, dict):
            source = dep.get("source")
            if source:
                deps.append(source)
    return deps


def local_ref(ref):
    return (
        ref.startswith("/")
        or ref.startswith(".")
        or ref.startswith("..")
        or "." not in ref
    )


seen = set()
include = set()


def visit(path):
    path = clean(path)
    if path in seen:
        return
    seen.add(path)

    config_path = "dagger.json" if path == "." else path + "/dagger.json"
    if not os.path.exists(posixpath.join(CONFIG_ROOT, config_path)):
        return

    include.add(path if path != "." else ".")
    include.add("dagger.json" if path == "." else path + "/dagger.json")
    include.add("**" if path == "." else path + "/**")

    for source in dependency_sources(config_path):
        if local_ref(source):
            visit(join(path, source))


visit(sys.argv[1] if len(sys.argv) > 1 else ".")

for path in sorted(include):
    print(path)
