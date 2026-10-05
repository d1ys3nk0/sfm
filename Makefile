.PHONY: check build lint lint-chk lint-fix lint-format lint-vet

PRE_COMMIT ?= pre-commit

check: lint-chk
	go run github.com/zricethezav/gitleaks/v8@v8.30.0 dir --no-banner .
	go mod tidy -diff
	go test -race ./...
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

build:
	@revision=$$(git rev-parse --verify HEAD 2>/dev/null || echo unknown); \
		test -z "$$(git status --porcelain --untracked-files=all)" || revision="$$revision-dirty"; \
		go build -trimpath -ldflags "-X main.version=dev -X main.commit=$$revision" -o bin/sfm ./cmd/sfm

lint: lint-fix lint-chk

lint-chk:
	$(PRE_COMMIT) run --hook-stage pre-commit --all-files

lint-fix:
	$(PRE_COMMIT) run --hook-stage manual --all-files >/dev/null || true

lint-format:
	@test -z "$$(gofmt -l cmd internal)"

lint-vet:
	go vet ./...
