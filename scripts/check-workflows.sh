#!/usr/bin/env bash
# Sanity-check the workflow files.
#
# A workflow missing its `on:` trigger is accepted by git and rejected by
# GitHub, which reports it as a run that fails in zero seconds with no job and
# no log - an unhelpful way to discover a truncated file. This caught exactly
# that: an edit that rewrote everything from `jobs:` onward dropped the `name:`,
# `on:` and `permissions:` header with it.
#
# Deliberately dependency-free: no yq, no actionlint, no pip install. It checks
# the few top-level keys whose absence is fatal, not the whole schema.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail=0
shopt -s nullglob
files=(.github/workflows/*.yml .github/workflows/*.yaml)

if [[ ${#files[@]} -eq 0 ]]; then
    echo "FAIL no workflow files found"
    exit 1
fi

for f in "${files[@]}"; do
    missing=()
    for key in name on jobs; do
        grep -qE "^${key}:" "$f" || missing+=("${key}")
    done

    if [[ ${#missing[@]} -gt 0 ]]; then
        echo "FAIL ${f}: missing top-level key(s): ${missing[*]}"
        fail=1
        continue
    fi

    # Tabs are not valid YAML indentation and are easy to introduce by hand.
    if grep -qP '^\t' "$f" 2>/dev/null; then
        echo "FAIL ${f}: contains a tab-indented line"
        fail=1
        continue
    fi

    echo "ok   ${f}"
done

exit "${fail}"
