BIN := shell3
MODELS_SNAPSHOT := internal/models/snapshot.json
MODELS_URL := https://models.dev/api.json

.PHONY: build build-offline models-snapshot run install clean

models-snapshot:
	curl -fsSL $(MODELS_URL) | python3 -c "\
import json,sys; d=json.load(sys.stdin); \
slim={m:v['limit']['context'] for p in d.values() for m,v in p.get('models',{}).items() if v.get('limit',{}).get('context')}; \
print(json.dumps(slim,indent=2))" > $(MODELS_SNAPSHOT)

build: models-snapshot
	go build -o $(BIN) ./cmd/$(BIN)

build-offline:
	go build -o $(BIN) ./cmd/$(BIN)

install:
	go install ./cmd/$(BIN)

run: build
	./$(BIN)

clean:
	rm -f $(BIN)
