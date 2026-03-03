BINARY    := kaggen
MODULE    := github.com/yourusername/kaggen
ENTRY     := ./cmd/kaggen
TAGS      := fts5
DIST      := dist

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS   := -s -w -X main.version=$(VERSION)

# Native build
.PHONY: build
build:
	CGO_ENABLED=1 go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BINARY) $(ENTRY)

# Pub/Sub bridge sidecar
.PHONY: build-bridge
build-bridge:
	go build -ldflags "$(LDFLAGS)" -o kaggen-pubsub-bridge ./cmd/kaggen-pubsub-bridge

# Cross-compilation targets (each requires native runner or cross-compiler)
.PHONY: build-darwin-arm64
build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 $(ENTRY)

.PHONY: build-darwin-amd64
build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64 $(ENTRY)

.PHONY: build-all
build-all: build-darwin-arm64 build-darwin-amd64

# Universal (fat) binary combining arm64 + amd64
.PHONY: build-universal
build-universal: build-darwin-arm64 build-darwin-amd64
	lipo -create -output $(DIST)/$(BINARY)-darwin-universal $(DIST)/$(BINARY)-darwin-arm64 $(DIST)/$(BINARY)-darwin-amd64

# Create release tarballs with checksums
.PHONY: release
release: build-universal
	cd $(DIST) && tar czf $(BINARY)-darwin-arm64.tar.gz $(BINARY)-darwin-arm64
	cd $(DIST) && tar czf $(BINARY)-darwin-amd64.tar.gz $(BINARY)-darwin-amd64
	cd $(DIST) && tar czf $(BINARY)-darwin-universal.tar.gz $(BINARY)-darwin-universal
	cd $(DIST) && shasum -a 256 *.tar.gz > checksums.txt

.PHONY: test
test:
	CGO_ENABLED=1 go test -tags "$(TAGS)" ./...

.PHONY: vet
vet:
	CGO_ENABLED=1 go vet -tags "$(TAGS)" ./...

.PHONY: clean
clean:
	rm -f $(BINARY) kaggen-pubsub-bridge
	rm -rf $(DIST)
