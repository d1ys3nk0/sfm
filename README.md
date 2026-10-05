# SFM — Synced File Manager

SFM captures selected files in a filesystem vault and installs them on another machine. It preserves permissions, symlinks, and empty directories, with ordered selection rules and conservative reconciliation. It supports macOS and Linux.

## Quick start

Install Go 1.27.1 and Task, then run `task build`. The executable is `bin/sfm`.

Create `~/.config/sfm/config.toml` using [the example](examples/config.toml). Use `sfm snapshot --dry` to preview a capture, `sfm snapshot` to apply it, and `sfm diff` to compare installed files. On another machine, `sfm install` creates missing files; `sfm install --force` replaces existing files of the same type after review.

```toml
vault = "~/file-vault"

[targets]
patterns = [
  "~/.config/editor/",
  "!~/.config/editor/cache/",
  "~/.config/editor/cache/settings.json",
]
```

## Commands

| Command | Behavior |
| --- | --- |
| `snapshot [--dry]` | Capture selected files; remove captured children deliberately deleted under an existing source directory. |
| `snapshot --dry --json` | Print an immutable preview as `{"entries": {"home/path": {"type": "file", "mode": 384, "hash": "…"}, "home/deleted": null}}`, including the `.sfm.json` fingerprint when it changes. |
| `install [--dry] [--force]` | Create missing files; force replaces differing content, permissions, and link targets. Type conflicts fail. |
| `diff [PATH]` | Compare all selected payloads and permissions, or only the given file/directory subtree, including unified text differences and binary summaries. |
| `verify` | Check missing selection roots, unexpected payloads, and metadata integrity. |
| `track PATH` | Add a literal selection and capture that subtree transactionally. Directory exclusions remain effective. |
| `forget PATH` | Remove scoped literal selections, add an exclusion when needed, and remove that captured subtree. Installed source files remain in place. |

An optional `diff` target accepts an absolute path, a path relative to the current directory, or a home-relative path beginning with `~/`. Directory targets include descendants, and the exit status reflects only that scope. The filesystem root, home directory itself, and vault are rejected as targets; omit `PATH` for a full comparison. Use `--config FILE` anywhere to select configuration. `--help` and `--version` work without configuration. Exit codes are 0 for success, 1 for differences or verification findings, and 2 for errors.

## Selection and safety

Patterns are ordered: a matching rule includes a path; `!` excludes it; a later match wins. `~/` identifies home paths and `/` identifies absolute paths. Bare paths also refer to home. Rules match from the namespace root. `*`, `?`, character classes, recursive `**` components, backslash escapes, and directory suffixes are supported. Selected directories include their descendants. Use `~/` for home paths rather than absolute aliases.

The vault stores files beneath `home/` and `root/`, with version-2 `.sfm.json` describing type, decimal POSIX mode, SHA-256 content hash, and symlink target. Other files at the vault root are outside SFM's responsibility. Unselected payloads are retained and reported by verification. Missing source roots never cause captured files to be removed. Directory deletion preserves retained children.

SFM rejects unsafe ancestors, traversal, vault self-selection, special file types, and file-type conflicts. It copies links without following them. Mutations prevalidate and freeze payloads, use atomic file replacement, and roll back filesystem changes on errors. Dry runs, diff, and verify write nothing. Selected content is shown only by explicit `diff`.

When a vault changes externally, review `diff` and reconcile with `install`. Ordinary installation preserves existing differences and does not acknowledge them. Incoming vault deletions require deliberate removal of the installed copy; SFM never deletes installed files for you. A vault with existing selected payloads needs an initial installation before capture.

## Configuration and state

Configuration defaults to `$XDG_CONFIG_HOME/sfm/config.toml`, or `~/.config/sfm/config.toml`. `vault` is required; `[targets].patterns` is an array of strings. Unknown configuration fields fail. Tracking edits preserve TOML comments and surrounding source formatting, and follow a configuration symlink to its real file.

State defaults to `$XDG_STATE_HOME/sfm/<vault-id>/`, or `~/.local/state/sfm/<vault-id>/`. The vault ID is the full lowercase SHA-256 of its canonical absolute path, without a trailing separator. `baseline.json` has version 2 and `entries` and `metadata` maps; `lock` serializes mutations. Keep state owner-private. A moved vault needs reconciliation at its new path.

The state is local to each checkout and machine. It records acknowledged filesystem state, so do not share it between machines or delete it casually.

## Development

Run `task check` for check-only pre-commit lint hooks, module consistency, race tests, vulnerability scanning, and secret scanning. `make lint` runs manual fixers followed by checks; `make lint-chk` runs check hooks only. CI invokes the underlying Go tools directly. `task fix:format` formats Go sources. `task build` produces a trimmed binary with version and revision metadata. Install local lint hooks with `pre-commit install`; see [CONTRIBUTING](CONTRIBUTING.md).

SFM is licensed under [MIT](LICENSE).
