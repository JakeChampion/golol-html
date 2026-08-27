#!/usr/bin/env bash
# Check the changelog fragments, and that a branch adds one rather than editing
# CHANGELOG.md.
#
# The second half is the point. Editing `## Unreleased` directly is how every
# open pull request came to conflict with every other one - the list is appended
# to at the same line by everyone, so a merge to main re-conflicts every branch
# still open. Nothing about that is visible while writing the entry; it shows up
# days later as four branches that will not merge, which is why it is a check
# rather than a note in a README.
#
# With --base <ref> it compares against that ref and fails on a CHANGELOG.md
# edit; without it, only the fragments themselves are checked, so a local run
# needs no remote. A diff that deletes fragments is a release fold and is
# allowed to edit CHANGELOG.md - that is the one commit that is supposed to.
#
# Deliberately shallow, like check-workflows.sh: a fragment is a bullet, free of
# conflict markers and tabs. It does not lint prose.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

base=""
if [[ ${1:-} == "--base" ]]; then
    base=${2:-}
    if [[ -z ${base} ]]; then
        echo "usage: $0 [--base <ref>]" >&2
        exit 2
    fi
    shift 2
fi
if [[ $# -gt 0 ]]; then
    echo "usage: $0 [--base <ref>]" >&2
    exit 2
fi

fail=0

shopt -s nullglob
fragments=(changelog.d/*)
shopt -u nullglob

for f in ${fragments[@]+"${fragments[@]}"}; do
    name=$(basename "${f}")
    [[ ${name} == "README.md" ]] && continue

    if [[ ${name} != *.md ]]; then
        echo "FAIL ${f}: not a .md file"
        fail=1
        continue
    fi

    if [[ ! -s ${f} ]]; then
        echo "FAIL ${f}: empty"
        fail=1
        continue
    fi

    # An entry that does not start with a bullet lands in the changelog as a
    # paragraph attached to whatever precedes it.
    if ! head -1 "${f}" | grep -q '^- '; then
        echo "FAIL ${f}: does not begin with a '- ' bullet"
        fail=1
        continue
    fi

    if grep -qE '^(<<<<<<<|>>>>>>>|=======$)' "${f}"; then
        echo "FAIL ${f}: contains conflict markers"
        fail=1
        continue
    fi

    if grep -qP '\t' "${f}" 2>/dev/null; then
        echo "FAIL ${f}: contains a tab"
        fail=1
        continue
    fi

    echo "ok   ${f}"
done

if [[ -n ${base} ]]; then
    # An added bullet is the thing to catch, not any edit: correcting a typo in
    # a released section conflicts with nobody, while adding an entry to the
    # Unreleased list is what every branch does at the same line.
    added=$(git diff -U0 "${base}" HEAD -- CHANGELOG.md | grep -c '^+- ' || true)
    if [[ ${added} -gt 0 ]]; then
        # A release fold deletes the fragments it consumed in the same commit,
        # and is the one change that is supposed to add entries here.
        if git diff --diff-filter=D --name-only "${base}" HEAD |
            grep -qE '^changelog\.d/.*\.md$'; then
            echo "ok   CHANGELOG.md edited by a release fold"
        else
            echo "FAIL CHANGELOG.md gained an entry directly."
            echo "     Put it in changelog.d/<pr>-<slug>.md instead - see" \
                "changelog.d/README.md."
            echo "     Every branch appends to the same point of the" \
                "Unreleased list, so an entry"
            echo "     added here conflicts with every other open branch," \
                "and again on each merge."
            fail=1
        fi
    else
        echo "ok   CHANGELOG.md gained no entries"
    fi
fi

exit "${fail}"
