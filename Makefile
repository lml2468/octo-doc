# octo-doc — developer tasks. Run `make help` for the list.

GO        ?= go
BINARY    ?= octo-doc
PKG       := ./...
BUILD_DIR := bin
LDFLAGS   := -s -w

# Storage backends for integration/e2e tests (override as needed).
export OCTO_TEST_DATABASE_URL ?= postgres://octo:octo@localhost:55432/octodoc
export OCTO_TEST_S3_BUCKET    ?= octo-test
export OCTO_TEST_S3_ENDPOINT  ?= http://localhost:59000
export OCTO_TEST_S3_ACCESS_KEY_ID     ?= minioadmin
export OCTO_TEST_S3_SECRET_ACCESS_KEY ?= minioadmin

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into bin/
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/octo-doc

.PHONY: build-octo
build-octo: ## Build the octo client CLI into bin/
	$(GO) build -ldflags "$(LDFLAGS) -X main.version=$(OCTO_VERSION)" -o $(BUILD_DIR)/octo ./cmd/octo

# Cross-compile the octo client for release. Emits bin/dist/octo_<os>_<arch>[.exe]
# and a SHA256SUMS the CLI's self-update verifies against.
OCTO_VERSION ?= dev
OCTO_PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64
.PHONY: release-octo
release-octo: ## Cross-compile octo for all release platforms + checksums
	@rm -rf $(BUILD_DIR)/dist && mkdir -p $(BUILD_DIR)/dist
	@for p in $(OCTO_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; out=octo_$${os}_$${arch}; \
		if [ "$$os" = "windows" ]; then out=$$out.exe; fi; \
		echo "  building $$out"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS) -X main.version=$(OCTO_VERSION)" \
			-o $(BUILD_DIR)/dist/$$out ./cmd/octo || exit 1; \
	done
	@cd $(BUILD_DIR)/dist && (command -v sha256sum >/dev/null 2>&1 && sha256sum * || shasum -a 256 *) > SHA256SUMS
	@echo "  wrote $(BUILD_DIR)/dist/SHA256SUMS"

.PHONY: run
run: ## Run the server locally
	$(GO) run ./cmd/octo-doc serve

.PHONY: test
test: ## Run all tests
	$(GO) test $(PKG)

.PHONY: test-race
test-race: ## Run tests with the race detector
	$(GO) test -race $(PKG)

.PHONY: cover
cover: ## Run tests with coverage, print a summary
	$(GO) test -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: cover-html
cover-html: cover ## Open the HTML coverage report
	$(GO) tool cover -html=coverage.out

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: fmt
fmt: ## Format all Go code
	gofmt -w .
	$(GO) mod tidy

.PHONY: vet
vet: ## Run go vet
	$(GO) vet $(PKG)

.PHONY: check
check: fmt vet lint test ## Run the full local quality gate

.PHONY: golden
golden: ## Regenerate golden fixtures (see docs/PORTING.md — requires the archived TS reference)
	@echo "Golden fixtures are frozen. See docs/PORTING.md for regeneration."

.PHONY: docker
docker: ## Build the Docker image
	docker build -f deploy/Dockerfile -t octo-doc:dev .

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) coverage.out
