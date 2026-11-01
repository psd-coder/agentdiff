#!/usr/bin/env bash

# validate.sh - lint, format, vet and test checks for this Go repository
#
# Usage: validate.sh [project-root] [flags]  --  see --help for the flag list.
#
# It keeps the flags and the output of the global validate skill, so the same
# instructions work in this repository: one PASS line on success, or a FAIL line
# followed by the output of each failing step, cut at VALIDATE_MAX_FAIL_LINES.
#
# golangci-lint runs first and alone, because with --fix it rewrites files that
# the other steps compile. The other steps only read the tree, so they run in
# parallel. The tests are the only slow step (about 30 s), so --quick skips them.

set -euo pipefail

DEADCODE="golang.org/x/tools/cmd/deadcode@v0.52.0"

usage() {
  cat <<'EOF'
validate.sh [project-root] [flags]

Runs golangci-lint (lint and gofmt), mygo vet, go mod tidy -diff, deadcode,
namelen (one-letter names) and the tests. Passing steps print nothing: success is one PASS line, failure is a
FAIL line plus the output of each failing step.

Moments (each exits at once when the repo's own git hook for that moment
already runs these checks):
  --pre-commit   about to commit          implies --check --quick
  --pre-push     about to push or open a PR

Flags:
  --quick        skip the tests
  --check        report formatting and lint issues, write nothing
  --help, -h     this text

Environment:
  VALIDATE_VERBOSE=1          also print the output of passing steps
  VALIDATE_MAX_FAIL_LINES=N   cut each failing step at N lines (default 100, 0 = no cut)

Exit codes:
  0  checks passed, or the hook for the moment runs them
  1  a check failed, or golangci-lint is missing
  2  unknown option
EOF
}

TARGET=""
QUICK=0
CHECK=0
GATE=""
for arg in "$@"; do
  case "$arg" in
    --quick) QUICK=1 ;;
    --check) CHECK=1 ;;
    --pre-commit)
      GATE="pre-commit"
      CHECK=1
      QUICK=1
      ;;
    --pre-push) GATE="pre-push" ;;
    --help | -h)
      usage
      exit 0
      ;;
    -*)
      echo "validate.sh: unknown option '$arg'" >&2
      exit 2
      ;;
    *) if [[ -z "$TARGET" ]]; then TARGET="$arg"; fi ;;
  esac
done

if [[ -t 1 ]]; then
  RED=$'\033[0;31m'
  GREEN=$'\033[0;32m'
  NC=$'\033[0m'
else
  RED=""
  GREEN=""
  NC=""
fi

VERBOSE="${VALIDATE_VERBOSE:-}"
MAX_FAIL_LINES="${VALIDATE_MAX_FAIL_LINES:-100}"

if [[ -n "$TARGET" ]]; then
  cd "$TARGET" || exit 1
fi

hook_is_gate() {
  local path body
  path=$(git rev-parse --git-path "hooks/$1" 2>/dev/null) || return 1
  [[ -x "$path" ]] || return 1
  body=$(grep -vE '^[[:space:]]*(#|$)' "$path" 2>/dev/null | grep -vE 'git[- ]lfs')
  [[ -n "$body" ]]
}

if [[ -n "$GATE" ]] && hook_is_gate "$GATE"; then
  echo "${GREEN}PASS${NC} gate=${GATE}-hook runs these checks itself"
  exit 0
fi

if ! command -v golangci-lint &>/dev/null; then
  echo "${RED}FAIL${NC} golangci-lint is not on PATH (brew install golangci-lint)"
  exit 1
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

lint_job() {
  if [[ $CHECK -eq 1 ]]; then
    golangci-lint run ./...
  else
    golangci-lint run --fix ./...
  fi
}

vet_job() { go tool mygo vet ./internal/app; }

tidy_job() { go mod tidy -diff; }

# namelen reports one-letter names. golangci-lint's varnamelen only checks names by their
# distance to later uses, so it misses short names used close to their declaration.
namelen_job() { go run ./tools/namelen .; }

# deadcode exits with 0 even when it finds unreachable functions.
deadcode_job() {
  local out
  out=$(go run "$DEADCODE" -test ./...)
  if [[ -n "$out" ]]; then
    printf '%s\n' "$out"
    return 1
  fi
}

test_job() { CGO_ENABLED=0 go test ./...; }

run_step() {
  local st=0
  "${1}_job" >"$TMP/$1.out" 2>&1 || st=$?
  printf '%s' "$st" >"$TMP/$1.st"
}

STEPS=(vet tidy deadcode namelen)
if [[ $QUICK -eq 0 ]]; then
  STEPS+=(test)
fi

set +e
run_step lint
PIDS=()
for step in "${STEPS[@]}"; do
  run_step "$step" &
  PIDS+=($!)
done
for pid in "${PIDS[@]}"; do
  wait "$pid"
done
set -e

clamp() {
  local file=$1 total
  total=$(wc -l <"$file" | tr -d ' ')
  if [[ "$MAX_FAIL_LINES" -eq 0 || "$total" -le "$MAX_FAIL_LINES" ]]; then
    cat "$file"
    return
  fi
  head -n "$MAX_FAIL_LINES" "$file"
  echo "... $((total - MAX_FAIL_LINES)) more lines dropped (VALIDATE_MAX_FAIL_LINES=0 for all)"
}

STATUS=()
FAILLOG=()
for step in lint "${STEPS[@]}"; do
  st=$(cat "$TMP/$step.st" 2>/dev/null || echo 1)
  if [[ $st -eq 0 ]]; then
    STATUS+=("${step}=ok")
    if [[ -n "$VERBOSE" ]]; then
      printf -- '--- %s (passed) ---\n%s\n' "$step" "$(cat "$TMP/$step.out")"
    fi
  else
    STATUS+=("${step}=FAIL")
    FAILLOG+=("--- ${step} (exit ${st}) ---" "$(clamp "$TMP/$step.out")")
  fi
done
if [[ $QUICK -eq 1 ]]; then
  STATUS+=("test=skipped:quick")
fi
if [[ $CHECK -eq 1 ]]; then
  STATUS+=("mode=check")
else
  STATUS+=("mode=fix")
fi

if [[ ${#FAILLOG[@]} -eq 0 ]]; then
  echo "${GREEN}PASS${NC} ${STATUS[*]}"
  exit 0
fi

echo "${RED}FAIL${NC} ${STATUS[*]}"
echo
printf '%s\n' "${FAILLOG[@]}"
exit 1
