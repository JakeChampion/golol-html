GO ?= go
REPO ?= JakeChampion/golol-html

.PHONY: all test race vet lint bench differential native native-all verify attest-verify tidy clean

all: test

test:
	$(GO) test ./...
	cd differential && $(GO) test ./...

race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

# Not `gofmt -l . | ... | (! read)`: that idiom aborts under macOS bash 3.2
# with set -e even when it passes.
lint: vet
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "unformatted files:"; echo "$$unformatted"; \
		gofmt -d .; \
		exit 1; \
	fi; \
	echo "all files are gofmt-clean"

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./...

# The differential tests are a separate module, so the root ./... misses them.
differential:
	cd differential && $(GO) test -count=1 ./...

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
# Requires the gh CLI. Finds nothing while the repository is private: GitHub
# does not support attestations for user-owned private repos. Archives predating
# the attestation step will also fail.
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
