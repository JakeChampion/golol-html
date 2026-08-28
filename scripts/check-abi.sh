#!/usr/bin/env bash
# The vendored header, the seven vendored archives and the cgo code describe one
# ABI, and this checks they still agree.
#
# C linkage carries no type information, so nothing catches a header that has
# drifted from the archive it claims to describe: internal/include/lol_html.h and
# internal/lib/*/liblolhtml.a are replaced by the same workflow but are separate
# files, and a header updated ahead of (or behind) an archive rebuild compiles
# cleanly and misbehaves - or fails to link - only on the affected platform, at
# user build time. Only linux/amd64 gets deep CI exercise, so a per-platform
# difference in the exported symbol set is exactly the kind of thing nobody
# notices. This checks the three sets against each other:
#
#   declared  - functions internal/include/lol_html.h declares
#   defined   - lol_html_* symbols each archive actually exports
#   called    - what the Go cgo code and shim.c/shim.h actually call
#
# What it cannot check is struct layout and calling convention: the archives ship
# without debug info, so nothing in them describes lol_html_str_t or
# lol_html_memory_settings_t. Those were verified by running a C probe against
# the linux/amd64 archive; on the other six the header's word is all there is.
#
# Deliberately dependency-free, like check-platforms.sh and check-pins.sh: nm,
# grep, sort, comm. The darwin and windows archives are Mach-O and COFF, which
# GNU nm on a Linux host cannot read, so llvm-nm is preferred where present and
# any archive no available tool can read is skipped with a notice rather than
# failing the run.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Symbols the archives export that the vendored header does not declare. Benign:
# cgo can only call what the header declares, so these are unreachable from the
# binding - but the set is pinned because a change in it means the header and the
# archives moved relative to each other, which is the drift this script exists to
# catch. If upstream's cbindgen output catches up, update this list (and consider
# whether the binding now wants Comment.Stream* / EndTag.Replace).
UNDECLARED_BUT_EXPORTED=(
    lol_html_comment_streaming_after
    lol_html_comment_streaming_before
    lol_html_comment_streaming_replace
    lol_html_end_tag_replace
)

HEADER=internal/include/lol_html.h

fail=0
work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT

# --- pick a symbol reader ----------------------------------------------------
#
# llvm-nm reads ELF, Mach-O and COFF; GNU nm on a Linux host reads only ELF.
readers=()
for candidate in llvm-nm nm; do
    if command -v "${candidate}" >/dev/null 2>&1; then
        readers+=("${candidate}")
    fi
done

if [[ ${#readers[@]} -eq 0 ]]; then
    echo "SKIP no nm or llvm-nm on this host; cannot read the vendored archives"
    exit 0
fi

# symbols <archive> -> defined external lol_html_* names, one per line, sorted.
# "Could not read it" has to be told apart from "read it and it exports nothing",
# because the second is drift and must fail rather than skip. An empty result is
# not the test: GNU nm reads part of a Mach-O archive and reports plugin noise on
# stdout for the rest, so it can produce output and still miss every real symbol.
# What it does say, unambiguously and only when it cannot parse the format, is
# "file format not recognized" - so that is the signal, and anything else
# (including nm's routine "no symbols" remarks) counts as a successful read.
#
# Only nm's own symbol lines are kept: an address, a one-letter type, a name.
# That drops the archive-member headers and plugin chatter both readers print.
# The leading underscore Mach-O prepends to C symbols is stripped so the seven
# platforms stay directly comparable.
UNREADABLE='file format not recognized|not recognized|unknown file type|invalid file type|bad format'

symbols() {
    local archive=$1 reader
    for reader in "${readers[@]}"; do
        "${reader}" --defined-only --extern-only "${archive}" \
            2> "${work}/err" > "${work}/raw" || true
        if grep -qiE "${UNREADABLE}" "${work}/err"; then
            continue
        fi
        grep -E '^[0-9a-fA-F]* +[A-Za-z] +[^ ]' "${work}/raw" \
            | awk '{ print $NF }' \
            | sed 's/^_//' \
            | grep -E '^(unstable_)?lol_html_[A-Za-z0-9_]+$' \
            | sort -u || true
        return 0
    done
    return 1
}

# --- defined: what each archive exports --------------------------------------

shopt -s nullglob
archives=(internal/lib/*/liblolhtml.a)

if [[ ${#archives[@]} -eq 0 ]]; then
    echo "FAIL no archives found under internal/lib"
    exit 1
fi

read_platforms=()
for archive in "${archives[@]}"; do
    platform=$(basename "$(dirname "${archive}")")
    if symbols "${archive}" > "${work}/def.${platform}"; then
        count=$(wc -l < "${work}/def.${platform}" | tr -d ' ')
        if [[ "${count}" -eq 0 ]]; then
            echo "FAIL ${platform}: archive is readable but exports no lol_html_* symbols at all"
            fail=1
            continue
        fi
        read_platforms+=("${platform}")
        echo "ok   ${platform}: ${count} exported lol_html_* symbols"
    else
        echo "SKIP ${platform}: no available nm could read $(basename "${archive}") on this host"
    fi
done

if [[ ${#read_platforms[@]} -eq 0 ]]; then
    echo "SKIP no archive on this host could be read; nothing checked"
    exit 0
fi

# --- the archives must agree with each other ---------------------------------

reference=${read_platforms[0]}
for platform in "${read_platforms[@]}"; do
    if ! diff -q "${work}/def.${reference}" "${work}/def.${platform}" >/dev/null; then
        echo "FAIL ${platform} exports a different lol_html_* symbol set than ${reference}"
        comm -23 "${work}/def.${reference}" "${work}/def.${platform}" \
            | sed "s/^/     only in ${reference}: /"
        comm -13 "${work}/def.${reference}" "${work}/def.${platform}" \
            | sed "s/^/     only in ${platform}: /"
        fail=1
    fi
done
if [[ ${fail} -eq 0 ]]; then
    echo "ok   the ${#read_platforms[@]} readable archives export an identical symbol set"
fi

# Anything missing from one archive is missing from the ABI, so check every
# claim against every archive that could be read, not just the reference.

# --- declared: what the header says exists -----------------------------------

if [[ ! -f "${HEADER}" ]]; then
    echo "FAIL ${HEADER} is missing"
    exit 1
fi

# Strip line comments (the doc comments name functions in prose), drop typedef
# lines (every typedef name ends in _t and would otherwise read as a call), then
# take each identifier that is followed by an open paren.
sed 's://.*::' "${HEADER}" \
    | grep -v '^[[:space:]]*typedef' \
    | grep -oE '\b(unstable_)?lol_html_[A-Za-z0-9_]+[[:space:]]*\(' \
    | tr -d '( \t' \
    | grep -vE '_t$' \
    | sort -u > "${work}/declared" || true

if [[ ! -s "${work}/declared" ]]; then
    echo "FAIL ${HEADER} declares no lol_html_* functions; the extraction is broken"
    exit 1
fi
echo "ok   ${HEADER} declares $(wc -l < "${work}/declared" | tr -d ' ') lol_html_* functions"

# --- called: what the binding actually reaches for ---------------------------
#
# Two spellings: C.lol_html_foo from Go, and bare lol_html_foo from the shim -
# where the name can appear as a macro argument rather than a call, so every
# identifier counts. Type names (all of which end in _t) are filtered out.
{
    grep -hoE '\bC\.(unstable_)?lol_html_[A-Za-z0-9_]+' ./*.go 2>/dev/null | sed 's/^C\.//'
    sed 's://.*::' shim.c shim.h 2>/dev/null | grep -oE '\b(unstable_)?lol_html_[A-Za-z0-9_]+'
} | grep -vE '_t$' | sort -u > "${work}/called" || true

if [[ ! -s "${work}/called" ]]; then
    echo "FAIL no lol_html_* calls found in the Go files or the shim; the extraction is broken"
    exit 1
fi
echo "ok   the binding calls $(wc -l < "${work}/called" | tr -d ' ') lol_html_* functions"

# --- called must be declared -------------------------------------------------

comm -23 "${work}/called" "${work}/declared" > "${work}/undeclared_call"
if [[ -s "${work}/undeclared_call" ]]; then
    echo "FAIL the binding calls functions ${HEADER} does not declare:"
    sed 's/^/     /' "${work}/undeclared_call"
    fail=1
fi

# --- called must be defined in EVERY archive ---------------------------------
#
# A symbol missing from one platform's archive is a link failure on that platform
# and nowhere else, which is why this is per-archive rather than against a union.

for platform in "${read_platforms[@]}"; do
    comm -23 "${work}/called" "${work}/def.${platform}" > "${work}/missing"
    if [[ -s "${work}/missing" ]]; then
        echo "FAIL ${platform}: the binding calls symbols this archive does not define:"
        sed 's/^/     /' "${work}/missing"
        echo "     a build for ${platform} would fail to link"
        fail=1
    fi
done

# --- declared must be defined in EVERY archive -------------------------------
#
# Not fatal to today's build, since only what the binding calls has to link, but
# it means the header is ahead of the binary: the next binding change to reach
# for one of these would break on that platform only.

for platform in "${read_platforms[@]}"; do
    comm -23 "${work}/declared" "${work}/def.${platform}" > "${work}/ahead"
    if [[ -s "${work}/ahead" ]]; then
        echo "FAIL ${platform}: ${HEADER} declares functions this archive does not define:"
        sed 's/^/     /' "${work}/ahead"
        echo "     the header is ahead of the archive; they were not rebuilt together"
        fail=1
    fi
done

# --- defined-but-not-declared, pinned ----------------------------------------

printf '%s\n' "${UNDECLARED_BUT_EXPORTED[@]}" | sort -u > "${work}/expected_extra"
comm -13 "${work}/declared" "${work}/def.${reference}" > "${work}/actual_extra"

if ! diff -q "${work}/expected_extra" "${work}/actual_extra" >/dev/null; then
    echo "FAIL the set of symbols exported but not declared by ${HEADER} has changed"
    comm -23 "${work}/expected_extra" "${work}/actual_extra" \
        | sed 's/^/     no longer exported undeclared: /'
    comm -13 "${work}/expected_extra" "${work}/actual_extra" \
        | sed 's/^/     newly exported undeclared: /'
    echo "     the header and the archives moved relative to each other;"
    echo "     update UNDECLARED_BUT_EXPORTED in ${BASH_SOURCE[0]} once that is understood"
    fail=1
else
    echo "ok   $(wc -l < "${work}/actual_extra" | tr -d ' ') exported symbols are undeclared by the header, as pinned"
fi


# --- struct and callback ABI, on the host platform only ----------------------
#
# Symbol names are only half the ABI. The other half - the layout of
# lol_html_str_t and lol_html_memory_settings_t, and the signatures of the
# callbacks the shim's trampolines hand to Rust - is what C linkage cannot check
# and what fails silently rather than at link time: a reordered struct field or a
# callback that returns the wrong width is memory corruption, not a build error.
#
# The archives ship without debug info, so nothing static describes their layout.
# The only way to check the header's word against the binary is to run it: build
# a probe from this header, link it against the archive, and see whether the
# library behaves the way the header says it will. That works for the host
# platform only. For the other six the header remains an unverified claim, which
# is worth knowing rather than assuming.

probe_platform() {
    local os arch
    case "$(uname -s)" in
        Linux) os=linux ;;
        Darwin) os=darwin ;;
        *) return 1 ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) return 1 ;;
    esac
    if [[ "${os}" == linux ]] && (ldd --version 2>&1 || true) | grep -qi musl; then
        echo "${os}_${arch}_musl"
    else
        echo "${os}_${arch}"
    fi
}

compiler=""
for candidate in cc gcc clang; do
    if command -v "${candidate}" >/dev/null 2>&1; then
        compiler=${candidate}
        break
    fi
done

host=$(probe_platform || true)

if [[ -z "${compiler}" ]]; then
    echo "SKIP no C compiler; struct and callback layout not checked against any archive"
elif [[ -z "${host}" || ! -f "internal/lib/${host}/liblolhtml.a" ]]; then
    echo "SKIP no vendored archive for this host; struct and callback layout not checked"
else
    case "${host}" in
        *_musl) link_flags=(-lc) ;;
        darwin_*) link_flags=(-liconv) ;;
        *) link_flags=(-lgcc_s -lutil -lrt -lpthread -lm -ldl -lc) ;;
    esac

    cat > "${work}/abiprobe.c" <<'PROBE'
// Built from internal/include/lol_html.h and linked against the host's vendored
// archive. Every check here is behavioural: it fails if the header describes a
// layout or a signature the archive does not actually implement.
#include <stdio.h>
#include <string.h>
#include "lol_html.h"

static int bad;
#define CHECK(cond, ...) do { if (!(cond)) { bad = 1; printf("     "); printf(__VA_ARGS__); printf("\n"); } } while (0)

static char out[1 << 16];
static size_t outn;
static void sink(const char *c, size_t n, void *ud) {
    CHECK(ud == (void *)0x5eed, "output sink got user_data %p, want 0x5eed", ud);
    if (n && outn + n <= sizeof out) { memcpy(out + outn, c, n); outn += n; }
}

static int stream_writes, stream_drops;
static int stream_write(lol_html_streaming_sink_t *s, void *ud) {
    stream_writes++;
    CHECK(ud == (void *)0xABCD, "streaming write got user_data %p, want 0xABCD", ud);
    return lol_html_streaming_sink_write_str(s, "!", 1, false);
}
static void stream_drop(void *ud) {
    stream_drops++;
    CHECK(ud == (void *)0xABCD, "streaming drop got user_data %p, want 0xABCD", ud);
}

static int elem_calls;
static lol_html_rewriter_directive_t on_elem(lol_html_element_t *el, void *ud) {
    elem_calls++;
    CHECK(ud == (void *)0x1234, "element handler got user_data %p, want 0x1234", ud);

    // lol_html_str_t returned by value: if data and len were transposed, the
    // length would read as a pointer and this would not match.
    lol_html_str_t name = lol_html_element_tag_name_get(el);
    CHECK(name.len == 1 && name.data && name.data[0] == 'p',
          "tag_name_get returned len=%zu, want the one byte \"p\"", name.len);
    lol_html_str_free(name);

    // Another by-value struct, this one two size_t fields.
    lol_html_source_location_bytes_t loc = lol_html_element_source_location_bytes(el);
    CHECK(loc.start == 0 && loc.end == 8,
          "source_location_bytes = {%zu,%zu}, want {0,8} for \"<p id=q>\"", loc.start, loc.end);

    CHECK(lol_html_element_has_attribute(el, "id", 2) == 1, "has_attribute(id) is not 1");
    CHECK(lol_html_element_has_attribute(el, "zz", 2) == 0, "has_attribute(zz) is not 0");

    // The streaming handler struct is read field by field by the library, so a
    // reordering here would call the wrong pointer rather than return an error.
    lol_html_streaming_handler_t h;
    h.user_data = (void *)0xABCD;
    h.write_all_callback = stream_write;
    h.drop_callback = stream_drop;
    h.reserved = NULL;
    CHECK(lol_html_element_streaming_append(el, &h) == 0, "streaming_append failed");
    return LOL_HTML_CONTINUE;
}

static lol_html_rewriter_directive_t stop_elem(lol_html_element_t *el, void *ud) {
    return LOL_HTML_STOP;
}

static const char DOC[] = "<p id=q>hi</p>";

// build_and_write runs one document through a rewriter built with the given
// memory settings, and reports the write result.
static int run(lol_html_element_handler_t handler, size_t max_mem, bool graceful,
               const char *body, size_t body_len) {
    lol_html_selector_t *sel = lol_html_selector_parse("p", 1);
    CHECK(sel != NULL, "selector_parse(\"p\") returned NULL");
    if (!sel) return -99;
    lol_html_rewriter_builder_t *b = lol_html_rewriter_builder_new();
    lol_html_rewriter_builder_add_element_content_handlers(
        b, sel, handler, (void *)0x1234, NULL, NULL, NULL, NULL);

    lol_html_memory_settings_t mem;
    mem.preallocated_parsing_buffer_size = 0;
    mem.max_allowed_memory_usage = max_mem;
    mem.graceful_bail_out_on_memory_limit_exceeded = graceful;

    // memory_settings is passed by value, so a wrong size or field order here
    // also shifts every argument after it.
    lol_html_rewriter_t *rw =
        lol_html_rewriter_build(b, "utf-8", 5, mem, sink, (void *)0x5eed, true);
    CHECK(rw != NULL, "rewriter_build returned NULL");
    int rc = -99;
    if (rw) {
        rc = lol_html_rewriter_write(rw, body, body_len);
        if (rc == 0) rc = lol_html_rewriter_end(rw);
        lol_html_rewriter_free(rw);
    }
    lol_html_rewriter_builder_free(b);
    lol_html_selector_free(sel);
    return rc;
}

int main(void) {
    // Unbuffered, so that what the probe managed to check still reaches the log
    // if a layout mismatch crashes it rather than merely failing a comparison.
    setvbuf(stdout, NULL, _IONBF, 0);

    // A library-allocated string comes back by value and reads as text.
    CHECK(lol_html_selector_parse("###", 3) == NULL, "selector_parse(\"###\") did not fail");
    lol_html_str_t e = lol_html_take_last_error();
    CHECK(e.data != NULL && e.len > 0 && e.len < 4096,
          "take_last_error returned data=%p len=%zu after a parse failure", (void *)e.data, e.len);
    if (e.data) CHECK(memchr(e.data, 0, e.len) == NULL, "error text is not the length it reports");
    lol_html_str_free(e);

    // And is empty, not stale, when nothing failed.
    lol_html_str_t none = lol_html_take_last_error();
    CHECK(none.data == NULL && none.len == 0,
          "take_last_error with no error returned data=%p len=%zu", (void *)none.data, none.len);

    // Handlers, unit getters and the streaming handler struct.
    outn = 0;
    int rc = run(on_elem, (size_t)-1, false, DOC, sizeof DOC - 1);
    CHECK(rc == 0, "rewrite returned %d, want 0", rc);
    CHECK(elem_calls == 1, "element handler ran %d times, want 1", elem_calls);
    CHECK(stream_writes == 1 && stream_drops == 1,
          "streaming callbacks ran write=%d drop=%d, want 1 and 1", stream_writes, stream_drops);
    CHECK(outn == strlen("<p id=q>hi!</p>") && memcmp(out, "<p id=q>hi!</p>", outn) == 0,
          "output was [%.*s], want [<p id=q>hi!</p>]", (int)outn, out);

    // LOL_HTML_STOP is the second enum value and really stops.
    outn = 0;
    rc = run(stop_elem, (size_t)-1, false, DOC, sizeof DOC - 1);
    CHECK(rc == -1, "a handler returning LOL_HTML_STOP gave %d, want -1", rc);
    lol_html_str_free(lol_html_take_last_error());

    // max_allowed_memory_usage is the SECOND field of the settings struct: if it
    // were read as the first, this would set a preallocation and never error.
    char big[1 << 13];
    memset(big, 'z', sizeof big);
    memcpy(big, "<p q=", 5);
    outn = 0;
    rc = run(on_elem, 16, false, big, sizeof big);
    CHECK(rc == -1, "a 16-byte memory cap gave %d, want -1", rc);
    lol_html_str_free(lol_html_take_last_error());
    size_t strict_bytes = outn;

    // graceful_bail_out is the THIRD field: turning it on must flush the bytes
    // the strict run threw away.
    outn = 0;
    rc = run(on_elem, 16, true, big, sizeof big);
    CHECK(rc == -1, "a 16-byte graceful memory cap gave %d, want -1", rc);
    lol_html_str_free(lol_html_take_last_error());
    CHECK(strict_bytes == 0 && outn > 0,
          "graceful bail-out flushed %zu bytes and strict flushed %zu; want some and none",
          outn, strict_bytes);

    if (!bad) printf("ABI-PROBE-OK\n");
    return bad;
}
PROBE

    if "${compiler}" -I internal/include -o "${work}/abiprobe" "${work}/abiprobe.c" \
        "internal/lib/${host}/liblolhtml.a" "${link_flags[@]}" > "${work}/cc.log" 2>&1
    then
        # Run through a subshell so that if a layout mismatch kills the probe,
        # the shell's "Segmentation fault" notice lands in the log with the rest
        # of the evidence instead of on this script's stderr.
        probe_status=0
        bash -c '"$1"; s=$?; exit "$s"' _ "${work}/abiprobe" \
            > "${work}/probe.log" 2>&1 || probe_status=$?
        if [[ ${probe_status} -eq 0 ]] && grep -q ABI-PROBE-OK "${work}/probe.log"; then
            echo "ok   ${host}: struct layout and callback signatures behave as ${HEADER} describes"
            echo "     (the other archives are not checked this way; nothing on this host can run them)"
        else
            echo "FAIL ${host}: the archive does not behave the way ${HEADER} describes"
            sed 's/^/     /' "${work}/probe.log"
            if [[ ${probe_status} -gt 128 ]]; then
                echo "     the probe died on signal $((probe_status - 128)) - a layout mismatch bad enough to crash"
            fi
            echo "     the header and the archive disagree on a struct layout or a callback signature"
            fail=1
        fi
    else
        echo "SKIP ${host}: probe did not build or link; struct layout not checked"
        sed 's/^/     /' "${work}/cc.log" | head -5
    fi
fi

if [[ ${fail} -eq 0 ]]; then
    echo "header, archives and cgo code describe the same ABI"
fi
exit "${fail}"
