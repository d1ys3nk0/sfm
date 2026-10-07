.PHONY: check build lint lint-chk lint-fix lint-format lint-vet release\:fix release\:feat

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

release\:fix release\:feat:
	@set -eu; \
		revision=$$(git rev-parse --verify HEAD); \
		tags=$$(git tag --points-at "$$revision" --list 'v*.*.*'); \
		if [ -n "$$tags" ]; then \
			printf 'Commit already has a version tag:\n%s\n' "$$tags"; \
			exit 0; \
		fi; \
		git fetch --tags origin; \
		tags=$$(git tag --points-at "$$revision" --list 'v*.*.*'); \
		if [ -n "$$tags" ]; then \
			printf 'Commit already has a version tag:\n%s\n' "$$tags"; \
			exit 0; \
		fi; \
		versions=$$(git tag --list --sort=-version:refname); \
		tag=$$(printf '%s\n' "$$versions" | awk -v target='$@' '\
			/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$$/ { \
				split(substr($$0, 2), version, "."); exit \
			} \
			END { \
				if (target == "release:feat") { version[2]++; version[3] = 0 } \
				else { version[3]++ } \
				printf "v%d.%d.%d\n", version[1], version[2], version[3] \
			}'); \
		git tag "$$tag" "$$revision"; \
		if ! git push origin "refs/tags/$$tag"; then \
			printf 'Tag %s remains local. Retry with: git push origin refs/tags/%s\n' "$$tag" "$$tag" >&2; \
			exit 1; \
		fi

lint: lint-fix lint-chk

lint-chk:
	$(PRE_COMMIT) run --hook-stage pre-commit --all-files

lint-fix:
	$(PRE_COMMIT) run --hook-stage manual --all-files >/dev/null || true

lint-format:
	@test -z "$$(gofmt -l cmd internal)"

lint-vet:
	go vet ./...
