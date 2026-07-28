BIN := shell3

# Version stamped into the binary via -ldflags. Falls back to "dev" when not
# in a git checkout or no tag exists.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build run install test test-webui webui lint fmt clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/$(BIN)

# Builds the web interface and stages it where internal/webui embeds it. The
# staged copy is committed so `go install` (which cannot run npm) still gets a
# working UI — re-run this whenever webui/src changes.
webui:
	cd webui && npm ci --silent && npm run build
	rm -rf internal/webui/dist
	cp -R webui/dist internal/webui/dist

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/$(BIN)

run: build
	./$(BIN)

test:
	go test -race -coverprofile=cover.out -covermode=atomic ./...
	@go tool cover -func=cover.out | tail -1

# Type-check and lint the web interface (the Go suite does not cover it).
test-webui:
	cd webui && npm ci --silent && npm run build && npm run lint

# Mirrors CI: formatting drift fails the build, then static analysis
# (go vet first, then the deeper golangci-lint suite).
lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	golangci-lint run ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BIN) cover.out
