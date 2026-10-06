# Contributing

Keep changes small, readable, and centered on filesystem behavior. Public contracts belong in documentation and behavioral tests; implementation details should remain private.

Install the tools in `.mise.toml` and run `make check` before submitting a change. Add meaningful coverage for changed selection, comparison, rollback, or safety behavior. Fixtures must be synthetic and must never inspect a contributor's real files.

Use Conventional Commits. Explain the observable problem, resulting behavior, and validation in a pull request. Keep dependencies minimal and justify additions. Install local lint hooks with `pre-commit install`; hooks invoke Makefile lint targets.

## Releases

Push a tag on a checked revision after the release workflow is merged into `main`:

```sh
git tag v0.1.0
git push origin v0.1.0
```

Tags must use `vMAJOR.MINOR.PATCH`, with numeric components, no leading zeros except zero itself, and no prerelease or build suffix. The Release workflow validates the tag, runs all Check jobs on the tagged revision (Linux/macOS checks, secret scanning, and snapshot packaging verification), and then publishes a GitHub Release using pinned GoReleaser v2.18.2. Packaging checks validate all four archives and SHA-256 checksums, then run the Linux AMD64 binary with `--version` to verify embedded version and commit metadata. Ordinary branch and pull-request checks continue independently.

Each release contains `sfm_<version>_<os>_<arch>.tar.gz` for Darwin/Linux and ARM64/AMD64, with `sfm`, `README.md`, and `LICENSE` inside, plus `checksums.txt` with SHA-256 hashes. The binary reports its version and commit through `--version`. Mac provisioning can consume these public Darwin archives once the first release exists.

For a transient workflow failure, open the failed Release run in GitHub Actions and choose **Re-run failed jobs** (or **Re-run all jobs** to repeat validation). Keep the tag at the same revision. For source or workflow fixes, merge the correction and push a new version tag; do not move a published tag.

With the pinned GoReleaser available locally, validate packaging without publishing:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

Snapshot artifacts are written to `dist/`; they are not published. Run `make check` before tagging.
