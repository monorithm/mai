# /// script
# requires-python = ">=3.13"
# dependencies = []
# ///
"""Examine a bustan project against the layout and import rules of the bustan-dev skill.

The project has src/<pkg>/modules/<module>/ for features and src/<pkg>/platform/
for shared providers. The script reads each Python file with ast and reports:

- a module folder that has no api/, application/, domain/ or infra/, no
  <module>_module.py or no __init__.py (FAIL)
- an import that crosses a layer the wrong way (FAIL)
- an import of a file inside a different module, past its __init__.py (FAIL)
- an import of a feature module from platform/ (FAIL)
- a file name that does not follow the names of the layer (warn)

Contract: standard library only. Exit codes: 0 with no FAIL, 1 with a FAIL,
2 for a usage error.

    uv run audit.py ~/code/my-api
    uv run audit.py ~/code/my-api --package my_api
"""

import argparse
import ast
import sys
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path

REQUIRED_LAYERS = ("api", "application", "domain", "infra")
# The layers that a file of each layer can import, in the same module.
ALLOWED = {
    "domain": {"domain"},
    "application": {"application", "domain"},
    "infra": {"infra", "domain"},
    "api": {"api", "application", "domain"},
    "listeners": {"listeners", "application", "domain", "jobs"},
    "jobs": {"jobs", "application", "domain"},
}
NAME_RULES = {
    "application": (
        ("_command.py", "_query.py"),
        "<request>_command.py or <request>_query.py",
    ),
    "listeners": (("on_",), "on_<event>.py"),
    "jobs": (("_job.py",), "<work>_job.py"),
}


@dataclass(frozen=True)
class Finding:
    level: str
    path: Path
    line: int
    text: str


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="audit.py", description=(__doc__ or "").splitlines()[0]
    )
    parser.add_argument("project", type=Path, help="the folder with pyproject.toml")
    parser.add_argument(
        "--package", help="the import package (default: the only one in src/)"
    )
    args = parser.parse_args(argv)
    project = args.project.expanduser().resolve()
    src = project / "src"
    if not src.is_dir():
        print(f"{src} does not exist", file=sys.stderr)
        return 2
    package = args.package or _only_package(src)
    if package is None:
        print("give --package: src/ has no package, or more than one", file=sys.stderr)
        return 2
    root = src / package
    findings = [*_structure(root), *_imports(root, package), *_names(root)]
    for finding in findings:
        where = f"{finding.path.relative_to(project)}:{finding.line}"
        print(f"{finding.level:4} {where}: {finding.text}")
    failed = sum(f.level == "FAIL" for f in findings)
    warned = sum(f.level == "warn" for f in findings)
    print(f"{len(_modules(root))} modules, {failed} FAIL, {warned} warn")
    return 1 if failed else 0


def _only_package(src: Path) -> str | None:
    packages = [p.name for p in src.iterdir() if (p / "__init__.py").exists()]
    return packages[0] if len(packages) == 1 else None


def _modules(root: Path) -> list[Path]:
    folder = root / "modules"
    if not folder.is_dir():
        return []
    return sorted(
        p for p in folder.iterdir() if p.is_dir() and not p.name.startswith(("_", "."))
    )


def _structure(root: Path) -> list[Finding]:
    findings = []
    if not (root / "modules").is_dir():
        findings.append(Finding("FAIL", root, 0, "no modules/ folder"))
    for module in _modules(root):
        for layer in REQUIRED_LAYERS:
            if not (module / layer / "__init__.py").exists():
                findings.append(Finding("FAIL", module, 0, f"no {layer}/__init__.py"))
        for name in (f"{module.name}_module.py", "__init__.py"):
            if not (module / name).exists():
                findings.append(Finding("FAIL", module, 0, f"no {name}"))
    return findings


def _imports(root: Path, package: str) -> list[Finding]:
    findings = []
    modules = {m.name for m in _modules(root)}
    for path in sorted(root.rglob("*.py")):
        parts = path.relative_to(root).parts
        tree = ast.parse(path.read_text(), filename=str(path))
        for line, target in _targets(tree, path, root, package):
            findings += _judge(path, parts, line, target, package, modules)
    return findings


def _targets(
    tree: ast.AST, path: Path, root: Path, package: str
) -> Iterator[tuple[int, str]]:
    """(line, absolute dotted name) of each import of the file."""
    relative = path.relative_to(root).with_suffix("").parts
    # An __init__.py is its package: "." in it means the package itself.
    here = [package, *(relative[:-1] if relative[-1] == "__init__" else relative)]
    is_init = relative[-1] == "__init__"
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                yield node.lineno, alias.name
        elif isinstance(node, ast.ImportFrom):
            if node.level:
                drop = node.level - 1 if is_init else node.level
                base = here[: len(here) - drop] if drop else here
                name = ".".join([*base, node.module] if node.module else base)
            else:
                name = node.module or ""
            yield node.lineno, name


def _judge(
    path: Path,
    parts: tuple[str, ...],
    line: int,
    target: str,
    package: str,
    modules: set[str],
) -> list[Finding]:
    names = target.split(".")
    in_project = names[0] == package
    if parts[0] == "platform":
        if in_project and len(names) > 1 and names[1] == "modules":
            return [
                Finding(
                    "FAIL", path, line, f"platform imports a feature module: {target}"
                )
            ]
        return []
    if parts[0] != "modules" or len(parts) < 3:
        return []
    module, layer = parts[1], parts[2]
    if layer not in ALLOWED:
        return []  # <module>_module.py and __init__.py compose the module
    if layer == "domain" and not in_project and names[0] not in sys.stdlib_module_names:
        return [Finding("FAIL", path, line, f"domain imports a library: {target}")]
    if not in_project or len(names) < 2:
        return []
    if names[1] == "platform":
        if layer == "domain":
            return [
                Finding("FAIL", path, line, f"domain imports the platform: {target}")
            ]
        return []
    if names[1] != "modules" or len(names) < 3:
        return []
    other = names[2]
    if other != module:
        if len(names) > 3 and other in modules:
            text = f"imports past the public names of {other}: {target}"
            return [Finding("FAIL", path, line, text)]
        if layer == "domain":
            return [
                Finding(
                    "FAIL", path, line, f"domain imports a different module: {target}"
                )
            ]
        return []
    if len(names) > 3:
        target_layer = names[3]
        if target_layer in ALLOWED and target_layer not in ALLOWED[layer]:
            return [
                Finding(
                    "FAIL", path, line, f"{layer}/ imports {target_layer}/: {target}"
                )
            ]
    return []


def _names(root: Path) -> list[Finding]:
    findings = []
    for module in _modules(root):
        for layer, (patterns, rule) in NAME_RULES.items():
            folder = module / layer
            if not folder.is_dir():
                continue
            for path in sorted(folder.glob("*.py")):
                if path.name == "__init__.py":
                    continue
                if not any(
                    path.name.startswith(p)
                    if p.endswith("_")
                    else path.name.endswith(p)
                    for p in patterns
                ):
                    findings.append(Finding("warn", path, 0, f"name is not {rule}"))
    return findings


if __name__ == "__main__":
    raise SystemExit(main())
