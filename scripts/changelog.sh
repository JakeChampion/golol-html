#!/usr/bin/env bash
# Fold changelog.d/*.md into CHANGELOG.md's Unreleased section.
#
# Entries live one per file so that two branches never edit the same lines;
# changelog.d/README.md says why. This is the other half: the step that turns
# them back into the single list a reader wants.
#
# Without --apply it prints the assembled section and changes nothing, which is
# what CI runs to prove the fragments still assemble.
#
# Deliberately dependency-free: no towncrier, no python, no yq. Fragments are
# passed through verbatim - the only decision this makes is what order they go
# in, which is the numeric prefix, and a file without one sorts last.
#
# Nothing here goes through `awk -v`: a fragment can contain a backslash (the
# existing changelog has `<p>\xe9</p>` in it) and -v interprets escapes in the
# value, so the entry that reached CHANGELOG.md would not be the one written.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

apply=0
if [[ ${1:-} == "--apply" ]]; then
    apply=1
    shift
fi
if [[ $# -gt 0 ]]; then
    echo "usage: $0 [--apply]" >&2
    exit 2
fi

shopt -s nullglob
fragments=(changelog.d/*.md)
shopt -u nullglob

# README.md documents the directory and is not an entry. Numeric prefix first
# and in order, then anything else alphabetically: `sort -V` gets both from one
# pass, unlike a plain sort, which puts 10- before 9-.
entries=()
if [[ ${#fragments[@]} -gt 0 ]]; then
    while IFS= read -r f; do
        [[ -n ${f} ]] && entries+=("${f}")
    done < <(printf '%s\n' "${fragments[@]}" |
        grep -v '^changelog\.d/README\.md$' | sort -V)
fi

if [[ ${#entries[@]} -eq 0 ]]; then
    echo "no entries in changelog.d/"
    exit 0
fi

# A blank line between entries, and none trailing: this is spliced into a file
# whose spacing is already right.
assembled=""
for f in "${entries[@]}"; do
    assembled+="$(cat "${f}")"$'\n\n'
done
assembled=${assembled%$'\n\n'}

if [[ ${apply} -eq 0 ]]; then
    printf '%s\n' "${assembled}"
    exit 0
fi

anchor="## Unreleased"
if ! grep -qxF "${anchor}" CHANGELOG.md; then
    echo "FAIL CHANGELOG.md has no '${anchor}' heading to fold into" >&2
    exit 1
fi
heading=$(grep -nxF "${anchor}" CHANGELOG.md | head -1 | cut -d: -f1)

# The heading is followed by an HTML comment saying entries live in
# changelog.d/. Insert below it, not above it: the note is about the section
# rather than part of it, and a folded entry that lands on top of it reads as
# though the note were the newest change.
insert=${heading}
total=$(wc -l <CHANGELOG.md)
while [[ ${insert} -lt ${total} ]]; do
    next=$(sed -n "$((insert + 1))p" CHANGELOG.md)
    if [[ -z ${next} ]]; then
        insert=$((insert + 1))
        continue
    fi
    if [[ ${next} == "<!--"* ]]; then
        while [[ ${insert} -lt ${total} ]]; do
            insert=$((insert + 1))
            if [[ $(sed -n "${insert}p" CHANGELOG.md) == *"-->"* ]]; then
                break
            fi
        done
        continue
    fi
    break
done

# The scan stops after the note, having stepped over the blank line below it;
# this reissues that blank rather than keeping both.
while [[ ${insert} -gt ${heading} ]] &&
    [[ -z $(sed -n "${insert}p" CHANGELOG.md) ]]; do
    insert=$((insert - 1))
done

# Newest entries lead the section, which is where they have always been added
# by hand. The tail's own leading blank lines are dropped and reissued here, so
# the spacing is the same whether or not the heading was followed by one.
tmp=$(mktemp)
trap 'rm -f "${tmp}"' EXIT
{
    head -n "${insert}" CHANGELOG.md
    echo
    printf '%s\n' "${assembled}"
    echo
    tail -n "+$((insert + 1))" CHANGELOG.md | sed '/./,$!d'
} >"${tmp}"
mv "${tmp}" CHANGELOG.md
trap - EXIT

rm -- "${entries[@]}"

if [[ ${#entries[@]} -eq 1 ]]; then
    echo "folded 1 entry into CHANGELOG.md:"
else
    echo "folded ${#entries[@]} entries into CHANGELOG.md:"
fi
printf '  %s\n' "${entries[@]}"
