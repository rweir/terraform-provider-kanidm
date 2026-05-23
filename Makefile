.PHONY: default build install test testacc test-acc test-acc-up test-acc-down test-acc-shell generate clean fmt lint help

default: build

# Build the provider
build:
	go build -o terraform-provider-kanidm

# Install the provider locally for testing
install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/ssoriche/kanidm/0.1.0/darwin_arm64/
	cp terraform-provider-kanidm ~/.terraform.d/plugins/registry.terraform.io/ssoriche/kanidm/0.1.0/darwin_arm64/

# Run unit tests
test:
	go test -v ./...

# Run acceptance tests (against whatever KANIDM_URL/KANIDM_TOKEN/SSL_CERT_FILE
# point at — usually `make test-acc-up` for a local Docker instance).
testacc: test-acc
test-acc:
	@if [ -z "$$KANIDM_URL" ] || [ -z "$$KANIDM_TOKEN" ]; then \
		echo "KANIDM_URL/KANIDM_TOKEN not set. Run 'make test-acc-up' then 'source test/.env'."; \
		exit 1; \
	fi
	TF_ACC=1 go test -tags=acc -v -timeout 30m ./internal/provider/...

# Bring up a fresh local Kanidm and bootstrap an RW token for acceptance tests.
# Writes test/.env, which you should `source` before running test-acc.
test-acc-up:
	./test/bootstrap.sh

# Stop the local acceptance-test Kanidm and wipe its data.
test-acc-down:
	cd test && docker compose down -v
	rm -rf test/data test/.env

# Open a shell in the running Kanidm container (debugging).
test-acc-shell:
	docker exec -it kanidm-acctest bash

# Generate provider code from OpenAPI schema
generate:
	@echo "Generating provider code from OpenAPI schema..."
	tfplugingen-openapi generate \
		--config internal/spec/generator_config.yml \
		--output internal/spec/provider_code_spec.json \
		internal/spec/kanidm-openapi.json
	tfplugingen-framework generate all \
		--input internal/spec/provider_code_spec.json \
		--output internal/provider

# Generate documentation
docs:
	tfplugindocs generate --provider-name kanidm

# Format code
fmt:
	go fmt ./...

# Run linter
lint:
	golangci-lint run

# Clean build artifacts
clean:
	rm -f terraform-provider-kanidm
	rm -f internal/spec/provider_code_spec.json
	rm -rf dist/

# Show help
help:
	@echo "Available targets:"
	@echo "  build      - Build the provider binary"
	@echo "  install    - Install the provider locally for testing"
	@echo "  test           - Run unit tests"
	@echo "  test-acc       - Run acceptance tests (requires KANIDM_URL and KANIDM_TOKEN)"
	@echo "  test-acc-up    - Bring up a local Kanidm + write test/.env"
	@echo "  test-acc-down  - Stop the local Kanidm + wipe its data"
	@echo "  test-acc-shell - Open a shell in the running Kanidm container"
	@echo "  generate   - Regenerate provider code from OpenAPI schema"
	@echo "  docs       - Generate documentation"
	@echo "  fmt        - Format code"
	@echo "  lint       - Run linter"
	@echo "  clean      - Remove build artifacts"
	@echo "  help       - Show this help message"
