GO ?= go
REPO ?= JakeChampion/golol-html

.PHONY: all test race vet lint bench differential properties platforms workflows modules pins changelog changelog-fold native native-all verify attest-verify tidy clean

all: test

test:
	$(GO) test ./...
	cd differential && $(GO) test ./...
	cd properties && $(GO) test ./...

# One invocation per module, like `test` and `vet` and for the same reason: ./...
# stops at a module boundary. CI runs the detector in all three, so covering only
# the root here would mean a race in the property or differential harnesses
# passes before a push and fails in CI.
race:
	$(GO) test -race -count=1 ./...
	cd differential && $(GO) test -race -count=1 ./...
	cd properties && $(GO) test -race -count=1 -rapid.checks=$(CHECKS) ./...

# One vet per module: ./... stops at a module boundary, so the root's invocation
# covers neither of the others. scripts/check-modules.sh keeps CI honest about
# the same thing.
vet:
	$(GO) vet ./...
	cd differential && $(GO) vet ./...
	cd properties && $(GO) vet ./...

# Not `gofmt -l . | ... | (! read)`: that idiom aborts under macOS bash 3.2
# with set -e even when it passes.
lint: vet platforms workflows modules pins changelog
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "unformatted files:"; echo "$$unformatted"; \
		gofmt -d .; \
		exit 1; \
	fi; \
	echo "all files are gofmt-clean"

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./...

# Check that every supported platform selects a link file and none falls
# through to the unsupported-platform guard.
platforms:
	scripts/check-platforms.sh

# Catch a workflow file that git accepts and GitHub rejects.
workflows:
	scripts/check-workflows.sh

# Catch a module that CI does not vet or test.
modules:
	scripts/check-modules.sh

# Catch a copy of the pinned lol-html revision that has drifted from the one in
# scripts/build-native.sh.
pins:
	scripts/check-pins.sh

# Check the changelog fragments. Pass BASE=origin/main to also check that this
# branch adds one rather than editing CHANGELOG.md, which is what CI does.
BASE ?=
changelog:
	scripts/check-changelog.sh $(if $(BASE),--base $(BASE))

# Fold changelog.d/*.md into CHANGELOG.md's Unreleased section. Run at release
# time; see changelog.d/README.md.
changelog-fold:
	scripts/changelog.sh --apply

# The differential tests are a separate module, so the root ./... misses them.
differential:
	cd differential && $(GO) test -count=1 ./...

# Property tests, likewise a separate module. Override the check count with
# CHECKS=20000 for a longer run.
CHECKS ?= 2000
properties:
	cd properties && $(GO) test -count=1 -rapid.checks=$(CHECKS) ./...

# Rebuild the vendored archive for the host platform.
native:
	scripts/build-native.sh

# Rebuild every supported platform. Needs the cross toolchains; CI does this.
native-all:
	scripts/build-native.sh --all

# Rebuild the host archive and check it matches what is committed.
verify:
	scripts/build-native.sh --verify

# Check each archive's signed provenance: which workflow run built it, from
# which commit. Unlike SHA256SUMS this cannot be forged by whoever pushes,
# because the signature is issued to the workflow rather than to the repo.
# Requires the gh CLI. Archives built before the attestation step existed, or
# built in a private fork (GitHub does not support attestations for user-owned
# private repositories), have nothing to verify and will fail.
#
# The header is checked with them: it is the ABI contract the cgo calls compile
# against, and the same workflow run replaces both. It joins the attestation
# subject and SHA256SUMS at the next rebuild, so until the native workflow has
# run once since that change there is nothing to verify for it either.
attest-verify:
	@for f in internal/lib/*/liblolhtml.a internal/include/lol_html.h; do \
		printf '==> %s\n' "$$f"; \
		gh attestation verify "$$f" --repo $(REPO) || exit 1; \
	done

tidy:
	$(GO) mod tidy

clean:
	$(GO) clean -cache -testcache
	rm -rf .native-build
