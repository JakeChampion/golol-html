GO ?= go

.PHONY: all test race vet lint bench native native-all verify tidy clean

all: test

test:
	$(GO) test ./...

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

# Rebuild the vendored archive for the host platform.
native:
	scripts/build-native.sh

# Rebuild every supported platform. Needs the cross toolchains; CI does this.
native-all:
	scripts/build-native.sh --all

# Rebuild the host archive and check it matches what is committed.
verify:
	scripts/build-native.sh --verify

tidy:
	$(GO) mod tidy

clean:
	$(GO) clean -cache -testcache
	rm -rf .native-build
