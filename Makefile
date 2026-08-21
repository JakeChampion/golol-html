GO ?= go

.PHONY: all test race vet lint bench native native-all verify tidy clean

all: test

test:
	$(GO) test ./...

race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

lint: vet
	gofmt -l . | tee /dev/stderr | (! read)

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
