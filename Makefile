BINARY  := aispend
VERSION ?= 0.1.0-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/prabhuvmk/aispend/internal/buildinfo

LDFLAGS := -s -w \
	-X '$(PKG).Version=$(VERSION)' \
	-X '$(PKG).Commit=$(COMMIT)' \
	-X '$(PKG).Date=$(DATE)'

.PHONY: build dev test vet check clean

build:
	go build -ldflags "$(LDFLAGS)" -o ./$(BINARY) .

## make dev ARGS="version --debug"  — rebuild and run in one keystroke
dev: build
	@./$(BINARY) $(ARGS)

test:
	go test ./...

vet:
	go vet ./...

check: build vet test

clean:
	rm -f ./$(BINARY)

TARGETS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64

## make release — cross-compile every target, with checksums.
## No C toolchain is involved: that is the whole reason for the pure-Go
## SQLite driver, and it is what makes this one command rather than a
## Docker-and-zig adventure.
.PHONY: release
release: clean
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		out=dist/$(BINARY)_$${os}_$${arch}; \
		if [ "$$os" = "windows" ]; then out=$$out.exe; fi; \
		echo "  $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags "$(LDFLAGS)" -o $$out . || exit 1; \
	done
	@cd dist && shasum -a 256 * > checksums.txt
	@echo
	@ls -lh dist
	@echo
	@echo "Sign before publishing:  cosign sign-blob --yes dist/checksums.txt"
