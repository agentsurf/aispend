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
