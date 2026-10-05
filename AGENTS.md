# Contributor guidance

Keep SFM a small filesystem tool. Separate path selection, reconciliation, and filesystem effects. Preserve other writers' changes and avoid speculative abstractions.

Use synthetic fixtures only. Never inspect personal files or place machine-specific configuration in this repository. Keep Go idiomatic and documentation paragraphs on single source lines. Run `make check` after changes.
