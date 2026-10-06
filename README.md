# SFM — Synced File Manager

SFM captures selected files in a filesystem vault and installs every vault payload on another machine. It preserves permissions, symlinks, and empty directories, with ordered selection rules and conservative reconciliation. It supports macOS and Linux.

## Quick start

Install Go 1.27.1, then run `make build`. The executable is `bin/sfm`.

Create `~/.config/sfm/config.toml` using [the example](examples/config.toml). Use `sfm snapshot --dry --diff` to preview a capture with content differences, then `sfm snapshot` to apply it. On another machine, `sfm install --dry --diff` previews installation; `sfm install` creates missing files and asks before replacing existing differences. Use `sfm install --force` to apply without asking.

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
| `snapshot [--dry] [--diff]` | Capture selected files automatically; remove captured children deliberately deleted under an existing source directory. Dry runs preview without writing; `--diff` adds content differences. |
| `install [--dry] [--diff] [--force]` | Install every vault payload regardless of capture rules. Create missing files and ask `y` or `n` for each existing differing path. `--force` skips questions. Dry runs show all changes without asking or writing. `--dry` and `--force` cannot be combined. Type conflicts fail. |
| `verify` | Check vault structure and supported filesystem types without inspecting installed files or capture selection. |
| `track PATH` | Add a literal selection and capture that subtree transactionally. Directory exclusions remain effective. |
| `forget PATH` | Remove scoped literal selections, add an exclusion when needed, and remove selected payloads in that subtree. Excluded payloads and their containing directories remain, as do installed files. |

`--diff` shows changes from current destination content to desired content, with three context lines and binary summaries. New files show their entire text as added lines; deleted vault files show removed lines. Operations, permission changes, and link changes are printed without `--diff`. Colors are automatic on terminals and redirected output stays plain. Use `--color=always` (or `--color`) to force Git-style colors, `--color=never` to disable them, or `--color=auto` for the default. Color options require `--diff`.

Installation collects every answer before applying one transaction. Answers are case-insensitive; invalid input repeats the question, and EOF or input errors abort all planned writes. Declined replacements remain unchanged and reconciliation stays pending. SFM rechecks sources, destinations, configuration, and baseline before applying, so edits made during questions abort the operation.

Use `--config FILE` anywhere to select configuration. `--help` and `--version` work without configuration. Exit codes are 0 for successful operations and previews, 1 for verification findings, and 2 for errors.

The standalone `diff` command and `--json` preview are removed. Replace comparison commands with `install --dry --diff` or `snapshot --dry --diff`; previews remain human-readable. There are no `--ask`, `--review`, or `-v` modes; ordinary installation asks automatically.

## Selection and safety

Patterns control capture, track, and forget; installation uses the complete vault independently of these rules. Patterns are ordered: a matching rule includes a path; `!` excludes it; a later match wins. `~/` identifies home paths and `/` identifies absolute paths. Bare paths also refer to home. Rules match from the namespace root. `*`, `?`, character classes, recursive `**` components, backslash escapes, and directory suffixes are supported. Selected directories include their descendants. Use `~/` for home paths rather than absolute aliases.

The vault is a payload-only directory tree: `vault/.config/app/file` installs to `~/.config/app/file`, and `vault/_/etc/app/file` installs to `/etc/app/file`. The real directory `_` is a structural container for absolute paths and is never itself installed. Home targets at `~/_` or below it are reserved; a wildcard that selects one fails. Root paths that alias home paths are rejected. Every other file or directory is a payload, including `.git/`, `.sfm.json`, and names such as `home/` and `root/`; keep administration files outside the vault.

Actual filesystem contents, permissions, and link targets are authoritative. There is no vault manifest or permission override. Preserve modes when transferring the vault; changing a payload's mode changes what installation applies. All payload directories, including absolute-path ancestors beneath `_`, are installed with their actual modes, so review directory permission changes as well as file changes.

Snapshot preserves unselected payloads, missing source roots, and directories containing retained children. Forget uses the selection rules in effect before its edit and preserves excluded descendants. Those retained payloads remain installable because installation ignores capture selection.

SFM rejects unsafe ancestors, traversal, vault self-selection, special file types, and file-type conflicts. It copies links without following them. Mutations prevalidate and freeze payloads, use atomic file replacement, and roll back filesystem changes on errors. Dry runs and verify write nothing. Selected content is shown only with explicit `--diff`, which adds output and does not prevent writes; combine it with `--dry` to preview.

When a vault changes externally, review `install --dry --diff` and reconcile with `install` or `install --force`. Refusing a replacement leaves it pending and does not acknowledge the new vault state. Incoming vault deletions require deliberate removal of the installed copy; SFM never deletes installed files for you. A vault with existing selected payloads needs an initial installation before capture. Snapshot preserves acknowledged fingerprints for unselected entries, so their incoming changes remain pending.

## Configuration and state

Configuration defaults to `$XDG_CONFIG_HOME/sfm/config.toml`, or `~/.config/sfm/config.toml`. `vault` is required; `[targets].patterns` is an array of strings. Unknown configuration fields fail. Tracking edits preserve TOML comments and surrounding source formatting, and follow a configuration symlink to its real file.

State defaults to `$XDG_STATE_HOME/sfm/<vault-id>/`, or `~/.local/state/sfm/<vault-id>/`. The vault ID is the full lowercase SHA-256 of its canonical absolute path, without a trailing separator. `baseline.json` has version 3 with an `entries` map of type, decimal POSIX mode, SHA-256 content hash, and link target; internal entry names remain `home/...` and `root/...` for stable reconciliation. There is no `metadata` map; `lock` serializes mutations. Keep state owner-private. A moved vault needs reconciliation at its new path.

The state is local to each checkout and machine. It records acknowledged filesystem state, so do not share it between machines or delete it casually.

## Migrating a version-2 vault

This layout is incompatible with the former `home/`, `root/`, and `.sfm.json` vault format. Back up the vault and each machine's private baseline before migration. Move the former `home/` contents into the vault root and the former `root/` contents beneath `_`; preserve current filesystem modes, empty directories, and links, and resolve any name collisions before moving files. Move the manifest out of the vault. Administration files left inside the vault become ordinary installable payloads.

Convert each local baseline to version 3 with only `version` and `entries`, keeping its acknowledged `home/...` and `root/...` identifiers and fingerprints rather than replacing the baseline with a scan of current incoming files. Older baselines are rejected with migration guidance. Review `install --dry --diff` after migration before reconciling; do not discard the baseline to bypass incoming-change checks.

## Development

Run `make check` for check-only pre-commit lint hooks, module consistency, race tests, vulnerability scanning, and secret scanning. `make lint` runs manual fixers followed by checks; `make lint-chk` runs check hooks only. CI invokes the underlying Go tools directly. `make lint-fix` formats Go sources. `make build` produces a trimmed binary with version and revision metadata. Install local lint hooks with `pre-commit install`; see [CONTRIBUTING](CONTRIBUTING.md).

SFM is licensed under [MIT](LICENSE).
