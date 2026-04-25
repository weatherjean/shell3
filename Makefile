BIN := shell3

.PHONY: build run clean

build:
	go build -o $(BIN) ./cmd/$(BIN)

run: build
	./$(BIN)

clean:
	rm -f $(BIN)
