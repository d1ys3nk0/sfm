# Contributing

Keep changes small, readable, and centered on filesystem behavior. Public contracts belong in documentation and behavioral tests; implementation details should remain private.

Install the tools in `.mise.toml` and run `make check` before submitting a change. Add meaningful coverage for changed selection, reconciliation, rollback, or safety behavior. Fixtures must be synthetic and must never inspect a contributor's real files.

Use Conventional Commits. Explain the observable problem, resulting behavior, and validation in a pull request. Keep dependencies minimal and justify additions. Install local lint hooks with `pre-commit install`; hooks invoke Makefile lint targets.
