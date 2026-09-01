BIN ?= $(HOME)/.local/bin/mesh

build:
	go build -o bin/mesh ./cmd/mesh

# Replace atomically rather than writing into the existing file: macOS
# invalidates the code signature of a binary modified in place and kills it on
# exec, silently, which looks exactly like a program that produces no output.
install: build
	@mkdir -p $(dir $(BIN))
	@cp bin/mesh $(BIN).new && mv -f $(BIN).new $(BIN)
	@echo "installed $(BIN)"

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin

.PHONY: build install test vet clean
