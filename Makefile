BIN := shell3

# Version stamped into the binary via -ldflags. Falls back to "dev" when not
# in a git checkout or no tag exists.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build run install test coverage lint fmt clean deepcheck acceptance

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/$(BIN)

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/$(BIN)

run: build
	./$(BIN)

test:
	go test -race -coverprofile=cover.out -covermode=atomic ./...
	@go tool cover -func=cover.out | tail -1

# Instruments every package for every test binary, so integration tests count
# code they exercise across package boundaries rather than only their own package.
coverage:
	go test -coverpkg=./... -coverprofile=cover-all.out ./...
	@go tool cover -func=cover-all.out | tail -1

# Mirrors CI: formatting drift fails the build, then static analysis
# (go vet first, then the deeper golangci-lint suite).
lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	golangci-lint run ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BIN) cover.out cover-all.out

deepcheck:
	./scripts/deepcheck.sh

acceptance: build
	./scripts/acceptance-local.sh ./$(BIN)
