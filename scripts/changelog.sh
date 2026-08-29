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
# Each fragment declares how much it moves the version, on a first line reading
#
#     <!-- bump: patch -->
#
# which is stripped when it is folded in. That is the one thing changesets has
# that a directory of prose does not: the release number stops being a judgement
# someone makes at the end, from a diff they have to reconstruct, and becomes the
# sum of what each change said about itself when the author still remembered.
# --bump prints the highest of them and --next-version applies it to the newest
# v* tag, which is what release.yml uses to name a release without being told.
#
# An HTML comment rather than the `---` frontmatter changesets uses: it cannot be
# mistaken for a horizontal rule, and a fragment that somehow reached CHANGELOG.md
# unstripped would be invisible there rather than drawing a line across it.
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

mode=print
case ${1:-} in
    --apply) mode=apply; shift ;;
    --release) mode=release; shift ;;
    --bump) mode=bump; shift ;;
    --next-version) mode=next; shift ;;
    "") ;;
    *) echo "usage: $0 [--apply | --release | --bump | --next-version]" >&2; exit 2 ;;
esac
if [[ $# -gt 0 ]]; then
    echo "usage: $0 [--apply | --release | --bump | --next-version]" >&2
    exit 2
fi

# The bump line, if a fragment has one. Only the first line is considered, so a
# comment further down is prose like any other.
bump_of() {
    local first
    first=$(head -1 "$1")
    if [[ ${first} =~ ^\<!--[[:space:]]*bump:[[:space:]]*(major|minor|patch)[[:space:]]*--\>$ ]]; then
        printf '%s' "${BASH_REMATCH[1]}"
    fi
}

# A fragment with its bump line taken off, and the blank line that followed it.
body_of() {
    if [[ -n $(bump_of "$1") ]]; then
        tail -n +2 "$1" | sed '/./,$!d'
    else
        cat "$1"
    fi
}

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
    if [[ ${mode} == bump || ${mode} == next ]]; then
        # Nothing to release. Said on stdout so a workflow can test for it
        # without parsing an error.
        echo "none"
        exit 0
    fi
    echo "no entries in changelog.d/"
    exit 0
fi

# The highest bump wins, which is the whole point of collecting them: one
# breaking change in a release of twenty makes the release breaking.
highest=patch
for f in "${entries[@]}"; do
    case $(bump_of "${f}") in
        major) highest=major ;;
        minor) [[ ${highest} != major ]] && highest=minor ;;
    esac
done

if [[ ${mode} == bump ]]; then
    echo "${highest}"
    exit 0
fi

if [[ ${mode} == next || ${mode} == release ]]; then
    # The newest v* tag by version order, not by date: a patch cut after a minor
    # is still the older number. Tags on an abandoned history sort with the rest,
    # which is right - they are still versions someone can `go get`.
    latest=$(git tag -l 'v[0-9]*' --sort=-v:refname | head -1)
    latest=${latest:-v0.0.0}
    IFS=. read -r major minor patch <<<"${latest#v}"
    case ${highest} in
        major) major=$((major + 1)); minor=0; patch=0 ;;
        minor) minor=$((minor + 1)); patch=0 ;;
        patch) patch=$((patch + 1)) ;;
    esac
    version="v${major}.${minor}.${patch}"
    if [[ ${mode} == next ]]; then
        echo "${version}"
        exit 0
    fi
fi

# A blank line between entries, and none trailing: this is spliced into a file
# whose spacing is already right.
assembled=""
for f in "${entries[@]}"; do
    assembled+="$(body_of "${f}")"$'\n\n'
done
assembled=${assembled%$'\n\n'}

if [[ ${mode} == print ]]; then
    printf '%s\n' "${assembled}"
    exit 0
fi

# --release is --apply plus the heading, because naming the section is the half
# of a release that was only ever written down in prose. `## Unreleased` stays
# where it is and the version goes underneath it, so the next change has
# somewhere to land without anyone moving anything.
if [[ ${mode} == release ]]; then
    if grep -qxF "## ${version}" CHANGELOG.md; then
        echo "FAIL CHANGELOG.md already has a '## ${version}' section" >&2
        exit 1
    fi
    assembled="## ${version}"$'\n\n'"${assembled}"
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
