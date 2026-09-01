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

race:
	go test -race ./...

# Cross-compile the binaries a mesh actually spans. A single static binary per
# platform is the point: nothing to install on the far machine but this file.
PLATFORMS = darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64
release:
	@rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "  $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w" \
			-o dist/mesh-$$os-$$arch$$ext ./cmd/mesh || exit 1; \
	done
	@ls -lh dist | tail -n +2 | awk '{print "  " $$9 "  " $$5}'

crosscheck:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		GOOS=$$os GOARCH=$$arch go build -o /dev/null ./... || exit 1; \
		echo "  ok $$os/$$arch"; \
	done

vet:
	go vet ./...

clean:
	rm -rf bin

.PHONY: build install test race vet clean release crosscheck
