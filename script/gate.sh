#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

run_contracts() {
  echo "== test-depth contracts =="
  python3 .github/workflows/coverage_gate_test.py
  python3 .github/workflows/gate_contract_test.py
}

run_coverage() {
  echo "== exact per-package coverage =="
  python3 .github/workflows/coverage_gate.py
}

run_fast_gate() {
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
}

run_fuzz() {
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
}

case "${1:-gate}" in
  gate)
    run_fast_gate
    ;;
  contracts)
    run_contracts
    ;;
  coverage)
    run_coverage
    ;;
  race)
    echo "== race =="
    go test -race -count=1 ./...
    ;;
  fuzz)
    echo "== bounded parser fuzz =="
    run_fuzz
    ;;
  *)
    echo "usage: $0 [gate|contracts|coverage|race|fuzz]" >&2
    exit 2
    ;;
esac
