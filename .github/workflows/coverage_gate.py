#!/usr/bin/env python3
"""Run each Go package once and enforce its own exact coverage floor."""

from __future__ import annotations

import json
import re
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
FLOOR_PATH = ROOT / "testing" / "coverage-floor.json"
TOTAL_RE = re.compile(r"^total:\s+\(statements\)\s+([0-9]+(?:\.[0-9]+)?)%$")


def load_floors(path: Path = FLOOR_PATH) -> dict[str, float]:
    data = json.loads(path.read_text(encoding="utf-8"))
    if data.get("schema_version") != 1:
        raise ValueError("coverage floor schema_version must be 1")
    packages = data.get("packages")
    if not isinstance(packages, dict) or not packages:
        raise ValueError("coverage floor packages must be a non-empty object")

    floors: dict[str, float] = {}
    for package, floor in packages.items():
        if not isinstance(package, str) or not package:
            raise ValueError("coverage floor package names must be non-empty strings")
        if isinstance(floor, bool) or not isinstance(floor, (int, float)):
            raise ValueError(f"coverage floor for {package} must be numeric")
        if not 0 < float(floor) <= 100:
            raise ValueError(
                f"coverage floor for {package} must be greater than 0 and at most 100"
            )
        floors[package] = float(floor)
    return floors


def discover_packages() -> list[str]:
    result = subprocess.run(
        ["go", "list", "./..."],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    packages = sorted(line.strip() for line in result.stdout.splitlines() if line.strip())
    if not packages:
        raise ValueError("go list ./... discovered no packages")
    return packages


def target_failures(discovered: list[str], floors: dict[str, float]) -> list[str]:
    discovered_set = set(discovered)
    configured_set = set(floors)
    failures = [
        f"package {package} has no coverage floor"
        for package in sorted(discovered_set - configured_set)
    ]
    failures.extend(
        f"coverage floor targets missing package {package}"
        for package in sorted(configured_set - discovered_set)
    )
    return failures


def parse_total(output: str) -> float:
    for line in output.splitlines():
        match = TOTAL_RE.match(line.strip())
        if match:
            return float(match.group(1))
    raise ValueError("go tool cover output has no total statement coverage")


def coverage_failures(actual: dict[str, float], floors: dict[str, float]) -> list[str]:
    return [
        f"{package}: coverage {actual[package]:.1f}% is below {floor:.1f}%"
        for package, floor in sorted(floors.items())
        if actual[package] < floor
    ]


def measure_packages(packages: list[str]) -> dict[str, float]:
    actual: dict[str, float] = {}
    with tempfile.TemporaryDirectory(prefix="music-coverage-") as temp_dir:
        for index, package in enumerate(packages):
            profile = Path(temp_dir) / f"package-{index}.out"
            subprocess.run(
                [
                    "go",
                    "test",
                    "-count=1",
                    "-covermode=set",
                    f"-coverprofile={profile}",
                    package,
                ],
                cwd=ROOT,
                check=True,
            )
            report = subprocess.run(
                ["go", "tool", "cover", f"-func={profile}"],
                cwd=ROOT,
                check=True,
                capture_output=True,
                text=True,
            )
            actual[package] = parse_total(report.stdout)
    return actual


def main() -> int:
    try:
        floors = load_floors()
        packages = discover_packages()
        failures = target_failures(packages, floors)
        if failures:
            raise ValueError("; ".join(failures))
        actual = measure_packages(packages)
        failures = coverage_failures(actual, floors)
        if failures:
            raise ValueError("; ".join(failures))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"coverage gate: FAIL: {error}", file=sys.stderr)
        return 1

    for package in packages:
        print(f"coverage: {package} {actual[package]:.1f}% >= {floors[package]:.1f}%")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
