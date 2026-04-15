.PHONY: setup install bench coverage release-dry-run

setup: ## Configure git hooks and install tools
	git config core.hooksPath .githooks
	@echo "Hooks activated from .githooks/"

install: ## Install mimic
	go build -o ~/.local/bin/mimic .

bench: ## Run microbenchmarks
	go test -bench=. -benchmem -count=3 -run='^$$' ./internal/... -timeout 120s

coverage: ## Collect test coverage
	scripts/coverage.sh

release-dry-run: ## Test release build locally (no publish)
	goreleaser release --snapshot --clean
