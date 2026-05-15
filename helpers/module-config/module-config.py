import argparse
import json
import os
import posixpath


CONFIG_PATH = "/dagger.json"
OUTPUT_PATH = "/out/dagger.json"
MOCK_ROOT = "/mock"
WORKSPACE_CONFIGS_PATH = "/workspace-configs"


def load_config():
    config = load_json(CONFIG_PATH)
    if not isinstance(config, dict):
        raise SystemExit("dagger.json must contain a JSON object")
    return config


def load_json(path):
    with open(path) as f:
        return json.load(f)


def dependency_source(dep):
    if isinstance(dep, str):
        return dep
    if isinstance(dep, dict):
        return dep.get("source") or ""
    return ""


def dependency_pin(dep):
    if isinstance(dep, dict):
        return dep.get("pin") or ""
    return ""


def write_config(config):
    write_json(OUTPUT_PATH, config)


def write_json(path, value):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        json.dump(value, f, indent=2)
        f.write("\n")


def write_text(path, value):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(value)


def clean_workspace_path(path):
    path = path.strip("/")
    if path in ("", "."):
        return "."

    path = posixpath.normpath(path)
    if path == ".":
        return "."
    if path == ".." or path.startswith("../") or posixpath.isabs(path):
        raise SystemExit(f"workspace path escapes root: {path}")
    return path


def module_config_path(module_path):
    module_path = clean_workspace_path(module_path)
    if module_path == ".":
        return "dagger.json"
    return posixpath.join(module_path, "dagger.json")


def is_local_source(source, pin=""):
    if pin or not source:
        return False
    return source[0] in "/." or "." not in source


def local_dependency_workspace_path(module_path, source):
    if posixpath.isabs(source):
        raise SystemExit(f"absolute local dependency source is not supported: {source}")

    base = "" if module_path == "." else module_path
    dep_path = posixpath.normpath(posixpath.join(base, source))
    return clean_workspace_path(dep_path)


def workspace_config(dep_path):
    path = posixpath.join(WORKSPACE_CONFIGS_PATH, module_config_path(dep_path))
    if not os.path.exists(path):
        raise SystemExit(f"local dependency has no dagger.json in workspace: {dep_path}")
    config = load_json(path)
    if not isinstance(config, dict):
        raise SystemExit(f"{dep_path}/dagger.json must contain a JSON object")
    return config


def workspace_module_name(dep_path):
    name = workspace_config(dep_path).get("name")
    if not isinstance(name, str) or name == "":
        raise SystemExit(f"local dependency {dep_path} has no name in dagger.json")
    return name


def dependency_explicit_name(dep):
    if isinstance(dep, dict):
        name = dep.get("name")
        if isinstance(name, str) and name != "":
            return name
    return ""


def write_synthetic_module(path, config):
    write_json(posixpath.join(MOCK_ROOT, module_config_path(path)), config)


def synthetic_directory(args):
    config = load_config()
    module_path = clean_workspace_path(args.module_path)
    write_text(posixpath.join(MOCK_ROOT, ".git", "HEAD"), "ref: refs/heads/main\n")
    write_synthetic_module(module_path, config)

    dependencies = config.get("dependencies") or []
    if not isinstance(dependencies, list):
        raise SystemExit("dagger.json dependencies must be an array")

    for dep in dependencies:
        source = dependency_source(dep)
        if not is_local_source(source, dependency_pin(dep)):
            continue

        dep_path = local_dependency_workspace_path(module_path, source)
        if dep_path == module_path:
            continue

        name = dependency_explicit_name(dep) or workspace_module_name(dep_path)
        write_synthetic_module(dep_path, {
            "name": name,
            "engineVersion": "latest",
        })


def updated_dependency_config(dep, source_root):
    kind = dep.get("kind")
    name = dep.get("moduleName")
    source = dep.get("asString")
    pin = dep.get("pin") or ""

    if not name or not source:
        raise SystemExit(f"malformed dependency in query response: {dep}")

    if kind == "LOCAL_SOURCE":
        source = posixpath.relpath(source, source_root)
    elif kind != "GIT_SOURCE":
        raise SystemExit(f"unsupported dependency kind in query response: {kind}")

    config = {
        "name": name,
        "source": source,
    }
    if pin:
        config["pin"] = pin
    return config


def replace_dependencies(args):
    config = load_config()
    response = load_json(args.response)
    if response.get("errors"):
        raise SystemExit(json.dumps(response["errors"], indent=2))

    try:
        dependencies = response["data"]["moduleSource"]["withUpdateDependencies"]["dependencies"]
    except (KeyError, TypeError) as err:
        raise SystemExit(f"query response did not include updated dependencies: {err}") from err

    if not isinstance(dependencies, list):
        raise SystemExit("query response dependencies must be an array")

    config["dependencies"] = [
        updated_dependency_config(dep, args.source_root)
        for dep in dependencies
    ]
    write_config(config)


def main():
    parser = argparse.ArgumentParser()
    subcommands = parser.add_subparsers(dest="command", required=True)

    synthetic_parser = subcommands.add_parser("synthetic")
    synthetic_parser.add_argument("--module-path", required=True)
    synthetic_parser.set_defaults(func=synthetic_directory)

    replace_parser = subcommands.add_parser("replace-dependencies")
    replace_parser.add_argument("--response", required=True)
    replace_parser.add_argument("--source-root", required=True)
    replace_parser.set_defaults(func=replace_dependencies)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
