BIN := shell3

.PHONY: build run install clean

build:
	go build -o $(BIN) ./cmd/$(BIN)

install:
	go install ./cmd/$(BIN)

run: build
	./$(BIN)

clean:
	rm -f $(BIN)
