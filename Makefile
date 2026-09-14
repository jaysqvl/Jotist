.PHONY: help dev frontend embed build build-cli test test-watch docs docs-clean docs-serve website-dev website-build website-serve clean

help: ## Show available commands
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

dev: ## Run the backend and Vite; use Air if already installed
	./scripts/dev.sh

frontend: ## Build the application frontend
	cd web/frontend && npm run build

embed: ## Copy an already built frontend into the Go embed directory
	./scripts/build.sh --embed-only

build: ## Build the frontend and server into bin/jotist
	./scripts/build.sh

build-cli: ## Build compatible scriberr CLI downloads
	mkdir -p bin/cli
	GOOS=linux GOARCH=amd64 go build -o bin/cli/scriberr-linux-amd64 ./cmd/scriberr-cli
	GOOS=darwin GOARCH=amd64 go build -o bin/cli/scriberr-darwin-amd64 ./cmd/scriberr-cli
	GOOS=darwin GOARCH=arm64 go build -o bin/cli/scriberr-darwin-arm64 ./cmd/scriberr-cli
	GOOS=windows GOARCH=amd64 go build -o bin/cli/scriberr-windows-amd64.exe ./cmd/scriberr-cli

test: ## Run Go tests (requires make embed or make build first)
	go tool gotestsum --format pkgname -- ./...

test-watch: ## Watch Go tests
	go tool gotestsum --watch -- ./...

docs: ## Generate the API package and project-site specifications
	./scripts/generate-api-docs.sh

docs-clean: ## Remove generated API specifications
	rm -f api-docs/docs.go api-docs/swagger.json api-docs/swagger.yaml
	rm -f web/project-site/public/api/swagger.json web/project-site/public/api/undocumented.json

website-dev: docs ## Run the project website in Vite
	cd web/project-site && npm run dev

website-build: docs ## Build the project website into web/project-site/dist
	cd web/project-site && npm run build

website-serve: website-build ## Build and preview the project website
	cd web/project-site && npm run preview

docs-serve: website-serve ## Alias for website-serve

clean: ## Remove local server, CLI, and frontend build outputs
	rm -rf bin internal/web/dist web/frontend/dist web/project-site/dist
