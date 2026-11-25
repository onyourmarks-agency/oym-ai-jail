# Changelog

All notable changes to this project are documented here. Releases are cut with
SemVer tags; binaries ship via GitHub Releases (`oym-ai-jail update`).

## [Unreleased]

### Added

- `oym-ai-jail` CLI: interactive TUI (huh), `run`, `shell`, `shell-init`,
  `status`, `print-profile`, `setup`, `update`, `uninstall`, `version`.
- macOS Seatbelt profiles generated from `~/.config/oym-ai-jail/config.yaml`:
  workspaces, `allow_workspace_read`/`allow_workspace_write`, `read_paths`,
  `write_paths`, `deny_read_paths` and per-agent config dirs — each agent sees
  only its own.
- Filesystem hardening: writable toolchain caches (go-build, npm/pnpm stores,
  cargo/gradle/maven/pub caches, Docker/OrbStack/Colima state) while anything
  on `PATH` or sourced by the shell stays read-only; executable dirs inside
  writable trees get write-denies that survive parent renames; credentials
  (`~/.config/gh` and friends, GPG keys) are not even readable.
- Agent presets for Claude Code (`claude`), Mistral Vibe (`vibe`) and OpenAI
  Codex (`codex`, with nested-sandbox workaround), plus `custom_agents`.
- Managed `~/.zshrc` block (`shell-init`) with migration of hand-installed
  `ai-shell` wrapper functions and `.oym-backup`/`.oym-backup-latest` backups.
- Fail-closed behaviour: a broken config emits blocking wrappers instead of
  running agents unjailed, and `AI_JAIL_ACTIVE` is verified with a real
  `sandbox-exec` probe rather than trusted.
- `install.sh` one-liner and GitHub Releases self-update, both verifying the
  release `CHECKSUM` (SHA-256).
