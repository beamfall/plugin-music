#!/usr/bin/env python3

import ast
import io
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest import mock


sys.path.insert(0, str(Path(__file__).resolve().parent))
import coverage_gate  # noqa: E402


SOURCE_PATH = Path(coverage_gate.__file__).resolve()
REQUIRED_MAIN_CALLS = (
    "load_floors",
    "discover_packages",
    "target_failures",
    "measure_packages",
    "coverage_failures",
)


def main_source_errors(source: str) -> list[str]:
    """Fail closed if main can bypass discovery, measurement, or enforcement."""
    try:
        tree = ast.parse(source)
    except SyntaxError as error:
        return [f"coverage main does not parse: {error}"]
    functions = [
        node
        for node in tree.body
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
        and node.name == "main"
    ]
    if len(functions) != 1:
        return ["coverage module must define exactly one main function"]
    main = functions[0]
    positions: dict[str, list[int]] = {name: [] for name in REQUIRED_MAIN_CALLS}
    success_returns: list[int] = []
    for node in ast.walk(main):
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
            if node.func.id in positions:
                positions[node.func.id].append(node.lineno)
        if (
            isinstance(node, ast.Return)
            and isinstance(node.value, ast.Constant)
            and node.value.value == 0
        ):
            success_returns.append(node.lineno)

    errors = [
        f"coverage main must call {name} exactly once"
        for name in REQUIRED_MAIN_CALLS
        if len(positions[name]) != 1
    ]
    if errors:
        return errors
    ordered = [positions[name][0] for name in REQUIRED_MAIN_CALLS]
    if ordered != sorted(ordered):
        errors.append("coverage main enforcement calls are out of order")
    if len(success_returns) != 1 or success_returns[0] <= ordered[-1]:
        errors.append("coverage main has an early or ambiguous success return")
    return errors


class CoverageGateTest(unittest.TestCase):
    def test_per_package_floor_rejects_aggregate_masking(self) -> None:
        actual = {"example/hot": 100.0, "example/cold": 80.0}
        floors = {"example/hot": 90.0, "example/cold": 90.0}

        self.assertEqual(
            coverage_gate.coverage_failures(actual, floors),
            ["example/cold: coverage 80.0% is below 90.0%"],
        )

    def test_discovery_rejects_unfloored_and_stale_packages(self) -> None:
        self.assertEqual(
            coverage_gate.target_failures(
                ["example/current", "example/new"],
                {"example/current": 80.0, "example/removed": 80.0},
            ),
            [
                "package example/new has no coverage floor",
                "coverage floor targets missing package example/removed",
            ],
        )

    def test_missing_total_is_rejected(self) -> None:
        with self.assertRaisesRegex(ValueError, "no total statement coverage"):
            coverage_gate.parse_total("example/file.go:1: function 100.0%")

    def test_boolean_and_zero_floors_are_rejected_as_vacuous(self) -> None:
        for value, error in ((True, "must be numeric"), (0.0, "greater than 0")):
            with self.subTest(value=value), tempfile.TemporaryDirectory() as temp_dir:
                floor_path = Path(temp_dir) / "coverage-floor.json"
                floor_path.write_text(
                    '{"schema_version": 1, "packages": {"example/package": '
                    + str(value).lower()
                    + "}}",
                    encoding="utf-8",
                )
                with self.assertRaisesRegex(ValueError, error):
                    coverage_gate.load_floors(floor_path)

    def test_main_executes_discovery_measurement_and_floor_enforcement(self) -> None:
        floors = {"example/package": 75.0}
        packages = ["example/package"]
        actual = {"example/package": 75.2}
        with (
            mock.patch.object(coverage_gate, "load_floors", return_value=floors) as load,
            mock.patch.object(
                coverage_gate, "discover_packages", return_value=packages
            ) as discover,
            mock.patch.object(
                coverage_gate, "target_failures", return_value=[]
            ) as targets,
            mock.patch.object(
                coverage_gate, "measure_packages", return_value=actual
            ) as measure,
            mock.patch.object(
                coverage_gate, "coverage_failures", return_value=[]
            ) as enforce,
            redirect_stdout(io.StringIO()),
            redirect_stderr(io.StringIO()),
        ):
            self.assertEqual(coverage_gate.main(), 0)
        load.assert_called_once_with()
        discover.assert_called_once_with()
        targets.assert_called_once_with(packages, floors)
        measure.assert_called_once_with(packages)
        enforce.assert_called_once_with(actual, floors)

    def test_main_fails_closed_on_package_discovery_drift(self) -> None:
        floors = {"example/package": 75.0}
        packages = ["example/package", "example/new"]
        with (
            mock.patch.object(coverage_gate, "load_floors", return_value=floors),
            mock.patch.object(coverage_gate, "discover_packages", return_value=packages),
            mock.patch.object(
                coverage_gate, "target_failures", return_value=["unfloored package"]
            ) as targets,
            mock.patch.object(coverage_gate, "measure_packages") as measure,
            mock.patch.object(coverage_gate, "coverage_failures") as enforce,
            redirect_stdout(io.StringIO()),
            redirect_stderr(io.StringIO()),
        ):
            self.assertEqual(coverage_gate.main(), 1)
        targets.assert_called_once_with(packages, floors)
        measure.assert_not_called()
        enforce.assert_not_called()

    def test_main_fails_closed_on_floor_regression(self) -> None:
        floors = {"example/package": 75.0}
        packages = ["example/package"]
        actual = {"example/package": 74.9}
        with (
            mock.patch.object(coverage_gate, "load_floors", return_value=floors),
            mock.patch.object(coverage_gate, "discover_packages", return_value=packages),
            mock.patch.object(coverage_gate, "target_failures", return_value=[]),
            mock.patch.object(
                coverage_gate, "measure_packages", return_value=actual
            ) as measure,
            mock.patch.object(
                coverage_gate, "coverage_failures", return_value=["below floor"]
            ) as enforce,
            redirect_stdout(io.StringIO()),
            redirect_stderr(io.StringIO()),
        ):
            self.assertEqual(coverage_gate.main(), 1)
        measure.assert_called_once_with(packages)
        enforce.assert_called_once_with(actual, floors)

    def test_main_source_contract_rejects_explicit_bypass_mutants(self) -> None:
        source = SOURCE_PATH.read_text(encoding="utf-8")
        self.assertEqual(main_source_errors(source), [])
        mutants = (
            (
                "def main() -> int:\n",
                "def main() -> int:\n    return 0\n",
                "early success",
            ),
            (
                "        packages = discover_packages()",
                "        packages = []",
                "discovery removed",
            ),
            (
                "        failures = target_failures(packages, floors)",
                "        failures = []",
                "discovery enforcement bypassed",
            ),
            (
                "        failures = coverage_failures(actual, floors)",
                "        failures = []",
                "floor enforcement bypassed",
            ),
        )
        for old, new, label in mutants:
            with self.subTest(mutant=label):
                self.assertIn(old, source)
                self.assertTrue(main_source_errors(source.replace(old, new, 1)))


if __name__ == "__main__":
    unittest.main(verbosity=2)
