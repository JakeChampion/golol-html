#!/usr/bin/env bash
# Every Go module in the tree has to be vetted and tested by CI.
#
# `go vet ./...` stops at a module boundary, so the root's invocation covers
# neither differential/ nor properties/. That is easy to forget when a module is
# added, and it went unnoticed once: properties/ was tested from the day it
# landed and never vetted, so a vet-detectable mistake in it - a Printf with the
# wrong argument, an unkeyed composite literal - would have reached main.
#
# Deliberately dependency-free, like check-platforms.sh and check-workflows.sh:
# it greps the workflow for a line naming each module rather than parsing YAML.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

ci=.github/workflows/ci.yml
fail=0

if [[ ! -f "$ci" ]]; then
    echo "FAIL $ci not found"
    exit 1
fi

# Module directories, relative and without the trailing /go.mod. The root is
# reported as "." and is covered by the plain `go vet ./...`.
while IFS= read -r mod; do
    dir=$(dirname "$mod")
    dir=${dir#./}

    if [[ "$dir" == "." ]]; then
        if ! grep -qE '^\s+- run: go vet \./\.\.\.$' "$ci"; then
            echo "FAIL $ci does not vet the root module"
            fail=1
        fi
        continue
    fi

    if ! grep -qE "cd ${dir} && go vet \./\.\.\." "$ci"; then
        echo "FAIL $ci does not vet the ${dir} module"
        echo "     add: - run: cd ${dir} && go vet ./..."
        fail=1
    fi

    # A module nobody runs the tests of is worse than one nobody vets.
    if ! grep -qE "working-directory: ${dir}" "$ci"; then
        echo "FAIL $ci has no job with working-directory: ${dir}"
        fail=1
    fi
done < <(find . -name go.mod -not -path './.native-build/*' -not -path './vendor/*' | sort)

if [[ $fail -eq 0 ]]; then
    echo "every module is vetted and tested"
fi
exit $fail
