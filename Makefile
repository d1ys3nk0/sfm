.PHONY: lint lint-chk lint-fix lint-format lint-vet

PRE_COMMIT ?= pre-commit

lint: lint-fix lint-chk

lint-chk:
	$(PRE_COMMIT) run --hook-stage pre-commit --all-files

lint-fix:
	$(PRE_COMMIT) run --hook-stage manual --all-files >/dev/null || true

lint-format:
	@test -z "$$(gofmt -l cmd internal)"

lint-vet:
	go vet ./...
