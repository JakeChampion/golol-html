GO ?= go
REPO ?= JakeChampion/golol-html

.PHONY: all test race vet lint bench differential properties platforms workflows native native-all verify attest-verify tidy clean

all: test

test:
	$(GO) test ./...
	cd differential && $(GO) test ./...
	cd properties && $(GO) test ./...

race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

# Not `gofmt -l . | ... | (! read)`: that idiom aborts under macOS bash 3.2
# with set -e even when it passes.
lint: vet platforms workflows
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
attest-verify:
	@for f in internal/lib/*/liblolhtml.a; do \
		printf '==> %s\n' "$$f"; \
		gh attestation verify "$$f" --repo $(REPO) || exit 1; \
	done

tidy:
	$(GO) mod tidy

clean:
	$(GO) clean -cache -testcache
	rm -rf .native-build
