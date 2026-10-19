#!/usr/bin/env python3
"""Fail-closed mutation proofs for the Music plugin test-depth gate."""

from __future__ import annotations

import copy
import json
import re
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parent))
from workflow_yaml import WorkflowYAMLError, loads as load_workflow_yaml


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github/workflows/conformance.yml"
GATE = ROOT / "script/gate.sh"
CHECKOUT_ACTION = "actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd"
SETUP_GO_ACTION = "actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff"
LINT_ACTION = (
    "golangci/golangci-lint-action@25e2cdc5eb1d7a04fdc45ff538f1a00e960ae128"
)
SDK_IF = "${{ inputs.sdk_ref != '' }}"
FUZZ_IF = (
    "${{ github.event_name == 'schedule' || "
    "github.event_name == 'workflow_dispatch' }}"
)
REQUIRED_FUZZ_TARGETS = {
    "FuzzParseFilename",
    "FuzzParseLastfmCSV",
    "FuzzParseLastfmEnvelopeJSON",
    "FuzzParseListenBrainzJSON",
}

CHECKOUT_PLUGIN = {
    "uses": CHECKOUT_ACTION,
    "with": {"path": "beamfall-plugin-music"},
}
CHECKOUT_SDK = {
    "if": SDK_IF,
    "uses": CHECKOUT_ACTION,
    "with": {
        "repository": "beamfall/plugin-sdk",
        "ref": "${{ inputs.sdk_ref }}",
        "path": "beamfall-plugin-sdk",
    },
}
SETUP_GO = {
    "uses": SETUP_GO_ACTION,
    "with": {"go-version-file": "beamfall-plugin-music/go.mod"},
}
USE_SDK = {
    "if": SDK_IF,
    "working-directory": "beamfall-plugin-music",
    "run": "go mod edit -replace github.com/beamfall/plugin-sdk=../beamfall-plugin-sdk",
}

JOB_SPECS = {
    "fast-conformance": {
        "fields": {"name": "fast conformance", "runs-on": "ubuntu-latest"},
        "steps": [
            ("Checkout plugin", CHECKOUT_PLUGIN),
            ("Checkout SDK override", CHECKOUT_SDK),
            ("Set up Go", SETUP_GO),
            ("Use SDK override", USE_SDK),
            (
                "gofmt",
                {
                    "working-directory": "beamfall-plugin-music",
                    "run": "gofmt -l . | tee /dev/stderr | (! grep -q .)",
                },
            ),
            (
                "vet",
                {
                    "working-directory": "beamfall-plugin-music",
                    "run": "go vet ./...",
                },
            ),
            (
                "test-depth contracts",
                {
                    "working-directory": "beamfall-plugin-music",
                    "run": "script/gate.sh contracts",
                },
            ),
            (
                "tests and exact per-package coverage",
                {
                    "working-directory": "beamfall-plugin-music",
                    "run": "script/gate.sh coverage",
                },
            ),
            (
                "golangci-lint",
                {
                    "uses": LINT_ACTION,
                    "with": {
                        "version": "v2.15.0",
                        "working-directory": "beamfall-plugin-music",
                    },
                },
            ),
        ],
    },
    "race": {
        "fields": {"runs-on": "ubuntu-latest"},
        "steps": [
            ("Checkout plugin", CHECKOUT_PLUGIN),
            ("Checkout SDK override", CHECKOUT_SDK),
            ("Set up Go", SETUP_GO),
            ("Use SDK override", USE_SDK),
            (
                "race",
                {
                    "working-directory": "beamfall-plugin-music",
                    "run": "script/gate.sh race",
                },
            ),
        ],
    },
    "conformance": {
        "fields": {
            "name": "conformance",
            "needs": ["fast-conformance", "race"],
            "if": "always()",
            "runs-on": "ubuntu-latest",
        },
        "steps": [
            (
                "Require fast and race jobs",
                {
                    "env": {
                        "FAST_RESULT": "${{ needs.fast-conformance.result }}",
                        "RACE_RESULT": "${{ needs.race.result }}",
                    },
                    "run": (
                        'test "$FAST_RESULT" = success\n'
                        'test "$RACE_RESULT" = success'
                    ),
                    "run_form": "|",
                },
            )
        ],
    },
    "fuzz": {
        "fields": {
            "if": FUZZ_IF,
            "runs-on": "ubuntu-latest",
            "timeout-minutes": 10,
        },
        "steps": [
            ("Checkout plugin", CHECKOUT_PLUGIN),
            ("Checkout SDK override", CHECKOUT_SDK),
            ("Set up Go", SETUP_GO),
            ("Use SDK override", USE_SDK),
            (
                "bounded parser fuzz",
                {
                    "working-directory": "beamfall-plugin-music",
                    "env": {"FUZZ_TIME": "20s"},
                    "run": "script/gate.sh fuzz",
                },
            ),
        ],
    },
}

EXPECTED_FUNCTIONS = {
    "run_contracts": """
echo "== test-depth contracts =="
python3 .github/workflows/coverage_gate_test.py
python3 .github/workflows/gate_contract_test.py
""",
    "run_coverage": """
echo "== exact per-package coverage =="
python3 .github/workflows/coverage_gate.py
""",
    "run_fast_gate": """
echo "== gofmt =="
fmt_out="$(gofmt -l .)"
if [ -n "$fmt_out" ]; then
echo "gofmt: files need formatting:"
echo "$fmt_out"
exit 1
fi
echo "== go vet =="
go vet ./...
run_contracts
run_coverage
echo "== golangci-lint =="
golangci-lint run ./...
echo "ALL GATES GREEN"
""",
    "run_fuzz": r"""
fuzz_time="${FUZZ_TIME:-20s}"
if [[ ! "$fuzz_time" =~ ^([1-9][0-9]*)(s|x)$ ]]; then
echo "fuzz: FUZZ_TIME must be a positive whole-second or execution budget, got $fuzz_time" >&2
exit 1
fi
fuzz_amount="${BASH_REMATCH[1]}"
fuzz_unit="${BASH_REMATCH[2]}"
if { [ "$fuzz_unit" = s ] && [ "$fuzz_amount" -gt 300 ]; } ||
{ [ "$fuzz_unit" = x ] && [ "$fuzz_amount" -gt 1000000 ]; }; then
echo "fuzz: FUZZ_TIME exceeds the bounded maximum (300s or 1000000x): $fuzz_time" >&2
exit 1
fi
expected="$(printf '%s\n' \
FuzzParseFilename \
FuzzParseLastfmCSV \
FuzzParseLastfmEnvelopeJSON \
FuzzParseListenBrainzJSON | LC_ALL=C sort)"
listed="$(go test -list '^Fuzz[A-Za-z0-9_]*$' .)"
targets="$(printf '%s\n' "$listed" | awk '/^Fuzz[A-Za-z0-9_]*$/ { print }' | LC_ALL=C sort)"
if [ "$targets" != "$expected" ]; then
echo "fuzz: expected exactly these targets:" >&2
printf '%s\n' "$expected" >&2
echo "fuzz: discovered:" >&2
printf '%s\n' "${targets:-none}" >&2
exit 1
fi
for target in FuzzParseFilename FuzzParseLastfmCSV FuzzParseLastfmEnvelopeJSON FuzzParseListenBrainzJSON; do
GOMAXPROCS=2 go test -run '^$' -fuzz "^${target}$" -fuzztime="$fuzz_time" .
done
""",
}

EXPECTED_ARMS = {
    "gate": """
run_fast_gate
;;
""",
    "contracts": """
run_contracts
;;
""",
    "coverage": """
run_coverage
;;
""",
    "race": """
echo "== race =="
go test -race -count=1 ./...
;;
""",
    "fuzz": """
echo "== bounded parser fuzz =="
run_fuzz
;;
""",
    "*": """
echo "usage: $0 [gate|contracts|coverage|race|fuzz]" >&2
exit 2
;;
""",
}


def uncommented(text: str) -> list[str]:
    return [line for line in text.splitlines() if not line.lstrip().startswith("#")]


def active_lines(text: str) -> tuple[str, ...]:
    return tuple(line.strip() for line in uncommented(text) if line.strip())


def steps(job: str) -> list[str]:
    found: list[list[str]] = []
    current: list[str] = []
    for line in uncommented(job):
        if line.startswith("      - "):
            if current:
                found.append(current)
            current = [line]
        elif current:
            current.append(line)
    if current:
        found.append(current)
    return ["\n".join(lines) for lines in found]


def name(step: str) -> str:
    found = re.findall(r"^      - name:\s*(.*?)\s*$", step, re.MULTILINE)
    return found[0] if len(found) == 1 else ""


def function(script: str, target: str) -> str:
    lines = script.splitlines()
    try:
        start = lines.index(f"{target}() {{") + 1
    except ValueError:
        return ""
    for end in range(start, len(lines)):
        if lines[end] == "}":
            return "\n".join(lines[start:end])
    return ""


def arm(script: str, target: str) -> str:
    lines = script.splitlines()
    try:
        start = lines.index(f"  {target})") + 1
    except ValueError:
        return ""
    body: list[str] = []
    for line in lines[start:]:
        if re.match(r"^  [A-Za-z0-9_*-]+\)$", line) or line == "esac":
            break
        body.append(line)
    return "\n".join(body)


def discover_repository_fuzz_targets(root: Path = ROOT) -> set[str]:
    targets: set[str] = set()
    for path in sorted(root.rglob("*_test.go")):
        source = path.read_text(encoding="utf-8")
        targets.update(
            re.findall(r"^func (Fuzz[A-Za-z0-9_]+)\(", source, re.MULTILINE)
        )
    return targets


def expected_gate_lines() -> tuple[str, ...]:
    expected = ['set -euo pipefail', 'cd "$(dirname "$0")/.."']
    for target, body in EXPECTED_FUNCTIONS.items():
        expected.append(f"{target}() {{")
        expected.extend(active_lines(body))
        expected.append("}")
    expected.append('case "${1:-gate}" in')
    for target, body in EXPECTED_ARMS.items():
        expected.append(f"{target})")
        expected.extend(active_lines(body))
    expected.append("esac")
    return tuple(expected)


def expected_workflow_model() -> dict:
    expected_jobs: dict[str, dict] = {}
    for job_name, job_spec in JOB_SPECS.items():
        job = copy.deepcopy(job_spec["fields"])
        job["steps"] = []
        for step_name, step_spec in job_spec["steps"]:
            step = {"name": step_name}
            step.update(
                copy.deepcopy(
                    {
                        key: value
                        for key, value in step_spec.items()
                        if key != "run_form"
                    }
                )
            )
            job["steps"].append(step)
        expected_jobs[job_name] = job
    return {
        "name": "conformance",
        "on": {
            "push": {"branches": ["main"], "tags": ["v*"]},
            "pull_request": None,
            "schedule": [{"cron": "43 5 * * 3"}],
            "workflow_dispatch": {
                "inputs": {
                    "sdk_ref": {
                        "description": "plugin-sdk ref to test against",
                        "required": False,
                        "type": "string",
                    }
                }
            },
        },
        "jobs": expected_jobs,
    }


def normalize_workflow(value, parent_key: str = ""):
    if isinstance(value, dict):
        return {
            key: normalize_workflow(child, key) for key, child in value.items()
        }
    if isinstance(value, list):
        return [normalize_workflow(child, parent_key) for child in value]
    if parent_key == "run" and isinstance(value, str):
        return value.rstrip("\n")
    return value


def first_difference(expected, actual, path: str = "$") -> str:
    if type(expected) is not type(actual):
        return (
            f"{path} type: expected {type(expected).__name__}, "
            f"got {type(actual).__name__}"
        )
    if isinstance(expected, dict):
        if set(expected) != set(actual):
            return (
                f"{path} keys: expected {sorted(expected)}, got {sorted(actual)}"
            )
        for key in expected:
            difference = first_difference(
                expected[key], actual[key], f"{path}.{key}"
            )
            if difference:
                return difference
        return ""
    if isinstance(expected, list):
        if len(expected) != len(actual):
            return f"{path} length: expected {len(expected)}, got {len(actual)}"
        for index, expected_item in enumerate(expected):
            difference = first_difference(
                expected_item, actual[index], f"{path}[{index}]"
            )
            if difference:
                return difference
        return ""
    if expected != actual:
        return f"{path}: expected {expected!r}, got {actual!r}"
    return ""


def workflow_errors(workflow: str) -> list[str]:
    try:
        actual = normalize_workflow(load_workflow_yaml(workflow))
    except WorkflowYAMLError as error:
        return [f"workflow YAML rejected: {error}"]
    difference = first_difference(
        normalize_workflow(expected_workflow_model()), actual
    )
    return [] if not difference else [f"workflow contract changed at {difference}"]


def isolated_workflow_parse(workflow: str) -> dict:
    parser_directory = str(WORKFLOW.parent)
    program = f"""
import json
import sys
sys.path.insert(0, {parser_directory!r})
from workflow_yaml import WorkflowYAMLError, loads
try:
    value = loads(sys.stdin.read())
except WorkflowYAMLError as error:
    result = {{"ok": False, "error": str(error)}}
else:
    result = {{"ok": True, "value": value}}
json.dump(result, sys.stdout, sort_keys=True)
"""
    completed = subprocess.run(
        [sys.executable, "-I", "-S", "-c", program],
        input=workflow,
        text=True,
        capture_output=True,
        check=True,
    )
    return json.loads(completed.stdout)


def gate_errors(gate: str, fuzz_targets: set[str]) -> list[str]:
    errors: list[str] = []
    lines = gate.splitlines()
    shebangs = [line for line in lines if line.startswith("#!")]
    if not lines or lines[0] != "#!/usr/bin/env bash" or shebangs != [
        "#!/usr/bin/env bash"
    ]:
        errors.append("gate must start with exactly one #!/usr/bin/env bash shebang")
    if active_lines(gate) != expected_gate_lines():
        errors.append("gate script has active commands outside its exact allowlist")
    for target, expected in EXPECTED_FUNCTIONS.items():
        if active_lines(function(gate, target)) != active_lines(expected):
            errors.append(f"gate function {target} active shape changed")
    labels = re.findall(r"^  ([A-Za-z0-9_*-]+)\)$", gate, re.MULTILINE)
    if labels != list(EXPECTED_ARMS):
        errors.append(f"gate mode labels changed: {labels}")
    for target, expected in EXPECTED_ARMS.items():
        if active_lines(arm(gate, target)) != active_lines(expected):
            errors.append(f"gate mode {target} active shape changed")
    if fuzz_targets != REQUIRED_FUZZ_TARGETS:
        errors.append(
            "repository fuzz targets changed: expected "
            f"{sorted(REQUIRED_FUZZ_TARGETS)}, got {sorted(fuzz_targets)}"
        )
    return errors


def contract_errors(
    workflow: str,
    gate: str,
    fuzz_targets: set[str],
) -> list[str]:
    return workflow_errors(workflow) + gate_errors(gate, fuzz_targets)


class GateContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.workflow = WORKFLOW.read_text(encoding="utf-8")
        cls.gate = GATE.read_text(encoding="utf-8")
        cls.fuzz_targets = discover_repository_fuzz_targets()

    def reject(
        self,
        workflow: str | None = None,
        gate: str | None = None,
        fuzz_targets: set[str] | None = None,
    ) -> None:
        self.assertTrue(
            contract_errors(
                self.workflow if workflow is None else workflow,
                self.gate if gate is None else gate,
                self.fuzz_targets if fuzz_targets is None else fuzz_targets,
            )
        )

    def job_parts(self, job_name: str) -> tuple[int, int, str]:
        match = re.search(
            rf"^  {re.escape(job_name)}:\n.*?(?=^  [A-Za-z0-9_-]+:\n|\Z)",
            self.workflow,
            flags=re.MULTILINE | re.DOTALL,
        )
        self.assertIsNotNone(match)
        assert match is not None
        return match.start(), match.end(), match.group(0)

    def mutate_job(self, job_name: str, old: str, new: str) -> str:
        start, end, block = self.job_parts(job_name)
        self.assertIn(old, block)
        return self.workflow[:start] + block.replace(old, new, 1) + self.workflow[end:]

    def step_block(self, job_name: str, step_name: str) -> str:
        _, _, block = self.job_parts(job_name)
        return next(step for step in steps(block) if name(step) == step_name)

    def test_live_contract(self) -> None:
        self.assertEqual(
            contract_errors(self.workflow, self.gate, self.fuzz_targets), []
        )

    def test_gate_shebang_is_exact_and_executable(self) -> None:
        shebang = "#!/usr/bin/env bash\n"
        self.assertTrue(GATE.stat().st_mode & stat.S_IXUSR)
        self.assertTrue(self.gate.startswith(shebang))
        mutants = {
            "different interpreter": self.gate.replace(
                shebang, "#!/bin/true\n", 1
            ),
            "interpreter options": self.gate.replace(
                shebang, "#!/usr/bin/env -S bash -e\n", 1
            ),
            "missing": self.gate.removeprefix(shebang),
            "duplicate": self.gate.replace(shebang, shebang + shebang, 1),
            "leading command": "true\n" + self.gate,
            "leading blank": "\n" + self.gate,
        }
        for label, mutant in mutants.items():
            with self.subTest(mutant=label):
                self.reject(gate=mutant)

    def test_workflow_triggers_are_exact(self) -> None:
        exact_flow = self.workflow.replace(
            "  push:\n    branches: [main]\n    tags: ['v*']",
            "  push: {branches: [main], tags: ['v*']}",
            1,
        )
        self.assertEqual(workflow_errors(exact_flow), [])
        exact_quoted = self.workflow.replace("  push:", '  "push":', 1)
        self.assertEqual(workflow_errors(exact_quoted), [])

        mutations = {
            "missing branches": self.workflow.replace(
                "    branches: [main]\n", "", 1
            ),
            "wrong branch": self.workflow.replace(
                "    branches: [main]", "    branches: [develop]", 1
            ),
            "missing tags": self.workflow.replace("    tags: ['v*']\n", "", 1),
            "wrong tags": self.workflow.replace(
                "    tags: ['v*']", "    tags: ['release-*']", 1
            ),
            "flow bypass": self.workflow.replace(
                "  push:\n    branches: [main]\n    tags: ['v*']",
                "  push: {branches: [develop], tags: ['v*']}",
                1,
            ),
            "missing pull request": self.workflow.replace(
                "  pull_request:\n", "", 1
            ),
            "conditioned pull request": self.workflow.replace(
                "  pull_request:\n", "  pull_request: {branches: [main]}\n", 1
            ),
            "missing schedule": self.workflow.replace(
                "  schedule:\n    - cron: '43 5 * * 3'\n", "", 1
            ),
            "wrong schedule": self.workflow.replace(
                "    - cron: '43 5 * * 3'", "    - cron: '0 0 * * *'", 1
            ),
            "missing dispatch": self.workflow.replace(
                "  workflow_dispatch:\n", "  disabled_dispatch:\n", 1
            ),
        }
        for label, mutant in mutations.items():
            with self.subTest(mutant=label):
                self.reject(workflow=mutant)

    def test_structural_parser_handles_quoted_and_flow_workflows(self) -> None:
        quoted_job = self.workflow.replace(
            "  fast-conformance:", '  "fast-conformance":', 1
        )
        self.assertEqual(workflow_errors(quoted_job), [])

        race_step = self.step_block("race", "race")
        exact_flow_step = (
            '      - {"name": race, '
            '"working-directory": beamfall-plugin-music, '
            '"run": "script/gate.sh race"}'
        )
        exact_flow = self.mutate_job("race", race_step, exact_flow_step)
        self.assertEqual(workflow_errors(exact_flow), [])

        bypass_flow_step = exact_flow_step.replace(
            '"script/gate.sh race"', '"echo bypass"'
        )
        self.reject(
            workflow=self.mutate_job("race", race_step, bypass_flow_step)
        )
        self.reject(
            workflow=self.workflow
            + '\n  "provider-live": {runs-on: ubuntu-latest, '
            'steps: [{run: "echo live"}]}\n'
        )
        self.reject(
            workflow=self.workflow
            + '\n  "race": {runs-on: ubuntu-latest, steps: []}\n'
        )

    def test_structural_parser_is_stdlib_only_and_isolated_results_match(self) -> None:
        variants = {
            "live": self.workflow,
            "quoted": self.workflow.replace(
                "  fast-conformance:", '  "fast-conformance":', 1
            ),
            "flow trigger": self.workflow.replace(
                "  push:\n    branches: [main]\n    tags: ['v*']",
                "  push: {branches: [main], tags: ['v*']}",
                1,
            ),
            "provider": self.workflow
            + '\n  "provider-live": {runs-on: ubuntu-latest, '
            'steps: [{run: "echo live"}]}\n',
            "duplicate": self.workflow
            + '\n  "race": {runs-on: ubuntu-latest, steps: []}\n',
        }
        for label, variant in variants.items():
            try:
                direct = {"ok": True, "value": load_workflow_yaml(variant)}
            except WorkflowYAMLError as error:
                direct = {"ok": False, "error": str(error)}
            with self.subTest(variant=label):
                isolated = isolated_workflow_parse(variant)
                self.assertEqual(isolated, direct)
                if label in {"live", "quoted", "flow trigger"}:
                    self.assertEqual(
                        first_difference(
                            normalize_workflow(expected_workflow_model()),
                            normalize_workflow(isolated["value"]),
                        ),
                        "",
                    )
                elif label == "provider":
                    self.assertIn("provider-live", isolated["value"]["jobs"])
                else:
                    self.assertFalse(isolated["ok"])

    def test_every_job_is_exact_active_and_fail_closed(self) -> None:
        for job_name, spec in JOB_SPECS.items():
            header = f"  {job_name}:\n"
            with self.subTest(job=job_name, mutant="continue"):
                self.reject(
                    self.mutate_job(
                        job_name,
                        header,
                        header + "    continue-on-error: true\n",
                    )
                )
            with self.subTest(job=job_name, mutant="duplicate"):
                _, _, block = self.job_parts(job_name)
                self.reject(workflow=self.workflow + "\n" + block)
            if "if" in spec["fields"]:
                old = f"    if: {spec['fields']['if']}"
                new = "    if: false"
            else:
                old = header
                new = header + "    if: false\n"
            with self.subTest(job=job_name, mutant="skip"):
                self.reject(self.mutate_job(job_name, old, new))

    def test_every_step_is_exact_active_unconditional_and_blocking(self) -> None:
        for job_name, spec in JOB_SPECS.items():
            for step_name, step_spec in spec["steps"]:
                block = self.step_block(job_name, step_name)
                header = f"      - name: {step_name}\n"
                with self.subTest(job=job_name, step=step_name, mutant="missing"):
                    self.reject(self.mutate_job(job_name, block, ""))
                with self.subTest(job=job_name, step=step_name, mutant="duplicate"):
                    self.reject(self.mutate_job(job_name, block, block + "\n" + block))
                with self.subTest(job=job_name, step=step_name, mutant="continue"):
                    self.reject(
                        self.mutate_job(
                            job_name,
                            header,
                            header + "        continue-on-error: true\n",
                        )
                    )
                if "if" in step_spec:
                    condition_old = f"        if: {step_spec['if']}"
                    condition_new = "        if: false"
                else:
                    condition_old = header
                    condition_new = header + "        if: false\n"
                with self.subTest(job=job_name, step=step_name, mutant="condition"):
                    self.reject(
                        self.mutate_job(
                            job_name,
                            condition_old,
                            condition_new,
                        )
                    )
                with self.subTest(job=job_name, step=step_name, mutant="comment"):
                    if "run" in step_spec and step_spec.get("run_form") == "|":
                        old = '          test "$RACE_RESULT" = success'
                        new = '          # test "$RACE_RESULT" = success'
                    elif "run" in step_spec:
                        run_form = step_spec.get("run_form", step_spec["run"])
                        old = f"        run: {run_form}"
                        new = f"        run: echo bypassed\n        # run: {run_form}"
                    else:
                        old = f"        uses: {step_spec['uses']}"
                        new = (
                            "        uses: actions/checkout@v5\n"
                            f"        # uses: {step_spec['uses']}"
                        )
                    self.reject(self.mutate_job(job_name, old, new))

    def test_arbitrary_provider_commands_and_jobs_are_rejected_by_allowlist(self) -> None:
        commands = (
            "curl https://api.listenbrainz.org",
            "bash -c 'wget https://ws.audioscrobbler.com'",
            "echo arbitrary-extra-step",
        )
        for job_name in JOB_SPECS:
            for command in commands:
                with self.subTest(job=job_name, command=command):
                    injected = (
                        "    steps:\n"
                        "      - name: injected provider command\n"
                        f"        run: {command}\n"
                    )
                    self.reject(
                        self.mutate_job(job_name, "    steps:\n", injected)
                    )
        extra_job = """
  provider-live:
    runs-on: ubuntu-latest
    steps:
      - name: live provider
        run: curl https://api.listenbrainz.org
"""
        self.reject(workflow=self.workflow + extra_job)

    def test_coverage_duplicate_dispatch_and_wrapper_forms(self) -> None:
        forms = (
            '        run: "script/gate.sh coverage"\n',
            "        run: |\n          script/gate.sh coverage\n",
            "        run: >\n          script/gate.sh coverage\n",
            "        run: bash -c 'script/gate.sh coverage'\n",
            (
                "        run: sh -c 'python3 "
                ".github/workflows/coverage_gate.py'\n"
            ),
        )
        anchor = "      - name: golangci-lint\n"
        for index, run_form in enumerate(forms):
            with self.subTest(form=index):
                duplicate = (
                    f"      - name: duplicate coverage {index}\n"
                    "        working-directory: beamfall-plugin-music\n"
                    + run_form
                )
                self.reject(
                    self.mutate_job(
                        "fast-conformance",
                        anchor,
                        duplicate + anchor,
                    )
                )
        for replacement in (
            '        run: "script/gate.sh coverage"',
            "        run: |\n          script/gate.sh coverage",
            "        run: >\n          script/gate.sh coverage",
        ):
            with self.subTest(replacement=replacement):
                equivalent = self.mutate_job(
                    "fast-conformance",
                    "        run: script/gate.sh coverage",
                    replacement,
                )
                self.assertEqual(workflow_errors(equivalent), [])
        self.reject(
            self.mutate_job(
                "fast-conformance",
                "        run: script/gate.sh coverage",
                "        run: bash -c 'script/gate.sh coverage'",
            )
        )
        self.reject(
            gate=self.gate.replace(
                "  run_coverage\n",
                "  bash -c 'python3 .github/workflows/coverage_gate.py'\n"
                "  run_coverage\n",
                1,
            )
        )

    def test_gate_early_success_and_wrapper_bypasses_are_rejected(self) -> None:
        function_mutants = (
            (
                "run_coverage() {\n",
                "run_coverage() {\n  return 0\n",
                "coverage return",
            ),
            (
                "run_coverage() {\n",
                "run_coverage() {\n  exit 0\n",
                "coverage exit",
            ),
            (
                "run_fuzz() {\n",
                "run_fuzz() {\n  return 0\n",
                "fuzz return",
            ),
            (
                "run_fuzz() {\n",
                "run_fuzz() {\n  true && return 0\n",
                "fuzz equivalent return",
            ),
        )
        for old, new, label in function_mutants:
            with self.subTest(mutant=label):
                self.reject(gate=self.gate.replace(old, new, 1))

        race_anchor = '  race)\n    echo "== race =="\n'
        for bypass in (
            "    exit 0\n",
            "    return 0\n",
            "    true && exit 0\n",
            "    if true; then exit 0; fi\n",
        ):
            with self.subTest(race_bypass=bypass.strip()):
                self.reject(
                    gate=self.gate.replace(
                        race_anchor,
                        race_anchor + bypass,
                        1,
                    )
                )
        self.reject(
            gate=self.gate.replace(
                "    go test -race -count=1 ./...",
                "    go test -race -count=1 ./... || true",
                1,
            )
        )
        self.reject(
            gate=self.gate.replace(
                "set -euo pipefail\n",
                "set -euo pipefail\nexit 0\n",
                1,
            )
        )
        self.reject(
            gate=self.gate.replace(
                'case "${1:-gate}" in',
                "run_coverage() { return 0; }\n\n"
                'case "${1:-gate}" in',
                1,
            )
        )

    def test_gate_commands_cannot_be_commented_conditional_or_nonblocking(self) -> None:
        mutations = (
            (
                "  python3 .github/workflows/coverage_gate.py",
                "  # python3 .github/workflows/coverage_gate.py",
            ),
            (
                "    go test -race -count=1 ./...",
                "    # go test -race -count=1 ./...",
            ),
            (
                "    GOMAXPROCS=2 go test -run '^$'",
                "    false && GOMAXPROCS=2 go test -run '^$'",
            ),
            (
                ' -fuzztime="$fuzz_time" .',
                ' -fuzztime="$fuzz_time" . || true',
            ),
        )
        for old, new in mutations:
            with self.subTest(mutant=new.strip()):
                self.assertIn(old, self.gate)
                self.reject(gate=self.gate.replace(old, new, 1))

    def test_action_pins_and_sdk_override_parity_are_exact(self) -> None:
        self.reject(
            workflow=self.workflow.replace(CHECKOUT_ACTION, "actions/checkout@v5", 1)
        )
        self.reject(
            workflow=self.workflow.replace(SETUP_GO_ACTION, "actions/setup-go@v5", 1)
        )
        for job_name in ("fast-conformance", "race", "fuzz"):
            for step_name in ("Checkout SDK override", "Use SDK override"):
                with self.subTest(job=job_name, step=step_name):
                    block = self.step_block(job_name, step_name)
                    self.reject(self.mutate_job(job_name, block, ""))

    def test_fuzz_budget_and_repository_wide_discovery_are_exact(self) -> None:
        self.reject(
            self.mutate_job("fuzz", "          FUZZ_TIME: 20s", "          FUZZ_TIME: 0s")
        )
        self.reject(
            self.mutate_job("fuzz", "    timeout-minutes: 10", "    timeout-minutes: 0")
        )
        self.reject(
            gate=self.gate.replace(
                'fuzz_time="${FUZZ_TIME:-20s}"',
                'fuzz_time="${FUZZ_TIME:-0s}"',
                1,
            )
        )
        self.reject(
            gate=self.gate.replace(
                "go test -list '^Fuzz[A-Za-z0-9_]*$' .",
                "printf bypassed",
                1,
            )
        )
        self.reject(fuzz_targets=self.fuzz_targets - {"FuzzParseLastfmCSV"})
        self.reject(
            fuzz_targets=self.fuzz_targets - {"FuzzParseLastfmEnvelopeJSON"}
        )
        self.reject(fuzz_targets=self.fuzz_targets | {"FuzzHiddenParser"})

    def test_repository_fuzz_discovery_reads_every_test_file(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            (root / "first_test.go").write_text(
                "package sample\nfunc FuzzFirst(f *testing.F) {}\n",
                encoding="utf-8",
            )
            nested = root / "nested"
            nested.mkdir()
            (nested / "second_test.go").write_text(
                "package nested\nfunc FuzzSecond(f *testing.F) {}\n",
                encoding="utf-8",
            )
            (root / "ignored.go").write_text(
                "package sample\nfunc FuzzIgnored(f *testing.F) {}\n",
                encoding="utf-8",
            )
            self.assertEqual(
                discover_repository_fuzz_targets(root),
                {"FuzzFirst", "FuzzSecond"},
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)
