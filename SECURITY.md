# Security

SFM reads and writes paths selected by your configuration. Review configuration and diffs before installing an unfamiliar vault. Keep configuration, vaults, and state writable only by trusted users. Filesystem checks cannot protect against a malicious process with the same account changing paths concurrently.

Report a suspected vulnerability privately to the maintainer through the repository's private security advisory channel when available. Do not include credentials, personal file contents, or private paths in public reports. Include a minimal synthetic reproducer, affected version, and expected behavior.
