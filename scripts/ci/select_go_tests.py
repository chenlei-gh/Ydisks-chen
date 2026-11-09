#!/usr/bin/env python3
"""Select Go test packages for pull requests, with conservative full-suite fallback."""

from __future__ import annotations

import json
import os
import subprocess
import sys
from collections import deque
from pathlib import Path
from typing import Any


def run(command: list[str], *, capture: bool = False) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else None,
    )


def is_documentation_path(path: str) -> bool:
    normalized = path.replace("\\", "/")
    if normalized.startswith(("docs/", "wiki/")):
        return True
    return "/" not in normalized and normalized.lower().endswith(".md")


def parse_json_stream(content: str) -> list[dict[str, Any]]:
    decoder = json.JSONDecoder()
    packages: list[dict[str, Any]] = []
    offset = 0
    while offset < len(content):
        while offset < len(content) and content[offset].isspace():
            offset += 1
        if offset >= len(content):
            break
        value, offset = decoder.raw_decode(content, offset)
        packages.append(value)
    return packages


def reverse_dependency_closure(
    packages: dict[str, dict[str, Any]], seeds: set[str], module_path: str
) -> set[str] | None:
    reverse: dict[str, set[str]] = {name: set() for name in packages}
    for import_path, package in packages.items():
        dependencies: set[str] = set()
        for field in ("Imports", "TestImports", "XTestImports"):
            dependencies.update(package.get(field) or [])
        for dependency in dependencies:
            if dependency in packages:
                reverse[dependency].add(import_path)
            elif dependency.startswith(module_path + "/"):
                # An in-module edge missing from the package list means the graph is incomplete.
                return None

    selected = set(seeds)
    pending = deque(seeds)
    while pending:
        dependency = pending.popleft()
        for dependent in reverse[dependency]:
            if dependent not in selected:
                selected.add(dependent)
                pending.append(dependent)
    return selected


def full_suite(reason: str) -> int:
    print(f"[go-tests] {reason}; fallback to go test ./...", flush=True)
    return subprocess.run(["go", "test", "./..."]).returncode


def changed_paths(base_sha: str) -> list[str]:
    if subprocess.run(
        ["git", "cat-file", "-e", f"{base_sha}^{{commit}}"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    ).returncode != 0:
        run(["git", "fetch", "--no-tags", "--depth=1", "origin", base_sha])
    result = run(["git", "diff", "--name-only", base_sha, "HEAD"], capture=True)
    return [line.strip() for line in result.stdout.splitlines() if line.strip()]


def main() -> int:
    if os.getenv("GITHUB_EVENT_NAME") != "pull_request":
        return full_suite("non-PR event")

    base_sha = os.getenv("PR_BASE_SHA", "").strip()
    if not base_sha:
        return full_suite("PR base SHA is unavailable")

    try:
        paths = changed_paths(base_sha)
    except (subprocess.CalledProcessError, OSError) as error:
        return full_suite(f"cannot determine changed files ({error})")

    if not paths:
        return full_suite("changed-file list is empty")

    code_paths = [path for path in paths if not is_documentation_path(path)]
    if not code_paths:
        print("[go-tests] documentation-only change; skip Go unit tests", flush=True)
        return 0

    if any(not path.endswith(".go") for path in code_paths):
        return full_suite("non-Go source/resource/configuration changed")

    try:
        listed = run(["go", "list", "-json", "./..."], capture=True)
        raw_packages = parse_json_stream(listed.stdout)
        module_path = run(["go", "list", "-m"], capture=True).stdout.strip()
    except (subprocess.CalledProcessError, json.JSONDecodeError, OSError) as error:
        return full_suite(f"cannot build Go package graph ({error})")

    packages: dict[str, dict[str, Any]] = {}
    directories: dict[str, str] = {}
    ignored_files: set[str] = set()
    for package in raw_packages:
        import_path = package.get("ImportPath")
        directory = package.get("Dir")
        if not import_path or not directory:
            return full_suite("go list returned an incomplete package record")
        packages[import_path] = package
        directories[os.path.normcase(os.path.abspath(directory))] = import_path
        for filename in package.get("IgnoredGoFiles") or []:
            ignored_files.add(os.path.normcase(os.path.abspath(os.path.join(directory, filename))))

    seeds: set[str] = set()
    for path in code_paths:
        absolute = os.path.normcase(os.path.abspath(path))
        if absolute in ignored_files:
            return full_suite(f"changed Go file is excluded by current build constraints: {path}")
        directory = os.path.normcase(os.path.abspath(os.path.dirname(path)))
        import_path = directories.get(directory)
        if not import_path:
            return full_suite(f"cannot map changed Go file to a package: {path}")
        seeds.add(import_path)

    selected = reverse_dependency_closure(packages, seeds, module_path)
    if not selected:
        return full_suite("package dependency graph is incomplete")

    package_list = sorted(selected)
    print(
        f"[go-tests] changed packages: {', '.join(sorted(seeds))}\n"
        f"[go-tests] testing {len(package_list)} of {len(packages)} packages",
        flush=True,
    )
    return subprocess.run(["go", "test", *package_list]).returncode


if __name__ == "__main__":
    sys.exit(main())
