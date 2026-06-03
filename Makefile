BIN := shell3

# Version stamped into the binary via -ldflags. Falls back to "dev" when not
# in a git checkout or no tag exists.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build run install test clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/$(BIN)

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/$(BIN)

run: build
	./$(BIN)

test:
	go test ./...

clean:
	rm -f $(BIN) webui
