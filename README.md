# oym-ai-jail

An AI coding agent should never see more of your machine than you explicitly allow.
`oym-ai-jail` enforces that. Not by trusting the agent's own guardrails, but by running agents like Claude Code, Mistral Vibe and OpenAI Codex inside a macOS Seatbelt sandbox. The OS draws the boundary: the agent writes only where you say, and everything else is invisible or read-only.

One binary. One command to install, configure and keep updated. No manual `.sb` files, no `sudo`, no hand-edited `~/.zshrc`.

## What it does

Running `claude` (or `vibe`, `codex`, …) wraps the process in `/usr/bin/sandbox-exec`
with a generated Seatbelt profile:

- **The agent can write only where you allow it** — by default the directory you
  started it in, plus your extra `write_paths`.
- **Siblings stay safe** — other projects in your workspace are invisible
  (`strict`), readable (`read`) or writable (`write`), your choice.
- **Your home dir is off-limits** — `/Users` and `/Volumes` are read+write denied
  except for the config dirs the agents need (`~/.claude`, `~/.codex`, …),
  writable toolchain caches (`go-build`, npm/pnpm stores, Docker state, …) and
  your `read_paths` (e.g. `~/ai-allowed`).
- **Nothing the jail depends on can be rewritten** — anything on `PATH` or
  sourced by your shell (`~/.local/bin`, version-manager dirs like
  `fnm`/`nvm`/`rvm`/`volta`/`sdkman`/`mise`, `~/go/bin`, `~/.orbstack/{bin,shell}`,
  `~/.docker/cli-plugins`, `.zshrc`, `.zcompdump`, `.zsh_history`,
  `~/.zsh_sessions`, `~/.config`, `~/.gnupg`) is read-only inside the jail, and
  writable dirs that hold executables (`~/.ddev/bin`, `~/.local/share/claude`,
  …) get an explicit write-deny — including their parents, so they can't be
  renamed out from under the rules. An agent can't plant code your next
  unjailed shell or command would run.
- **The jail protects itself** — `~/.config/oym-ai-jail` (or wherever
  `OYM_AI_JAIL_CONFIG` points), the `oym-ai-jail` binary *and* the directory it
  lives in can never be written from inside the sandbox, even by an agent that
  gets prompt-injected into trying.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/onyourmarks-agency/oym-ai-jail/main/install.sh | bash
```

This drops the `oym-ai-jail` binary in `/usr/local/bin` (or `~/.local/bin`,
override with `INSTALL_DIR`) and opens the interactive setup immediately.
One command, done.

Alternatively: `go install github.com/onyourmarks-agency/oym-ai-jail@latest`, or
download an archive from the releases page.

## Configure

```bash
oym-ai-jail          # interactive TUI (agents, isolation mode, paths)
oym-ai-jail setup --yes   # non-interactive: detected defaults
```

Setup writes `~/.config/oym-ai-jail/config.yaml` and wires one eval line into
`~/.zshrc` between `# >>> oym-ai-jail >>>` markers. Everything is dynamic after
that: toggling agents or editing the YAML takes effect in the next shell, no
rc-file edits needed. Old hand-installed `ai-shell` wrapper functions are
detected and can be removed from the TUI. Every `.zshrc` rewrite keeps a
`~/.zshrc.oym-backup` (the original, once) and a `~/.zshrc.oym-backup-latest`
(the previous state, rolling).

```yaml
version: 1
workspaces: # project roots; cwd inside one = writable
  - ~/dev/oym-docker
  - ~/dev/automation
allow_workspace_read: true # siblings readable?
allow_workspace_write: false # siblings writable? (implies read)
read_paths: # always read-only, even inside writable workspaces
  - ~/ai-allowed
write_paths: [] # always writable
deny_read_paths: [] # never even readable — extends the built-in credential list
agents:
  claude: true
  vibe: true
  codex: false
custom_agents: # wrap any other CLI
  - name: aider
    rw_paths: [~/.aider]
```

## Commands

| Command                          | What it does                                                   |
| -------------------------------- | -------------------------------------------------------------- |
| `oym-ai-jail`                    | Interactive TUI: enable agents, set isolation, manage paths    |
| `oym-ai-jail setup`              | Wire the `.zshrc` block (`--yes` also seeds detected defaults) |
| `oym-ai-jail run <agent> [args]` | Run an agent inside the jail (used by the zsh wrappers)        |
| `oym-ai-jail shell`              | Interactive zsh inside the jail (the old `ai-shell` behavior)  |
| `oym-ai-jail status`             | Effective jail for the current directory                       |
| `oym-ai-jail print-profile`      | Dump the generated Seatbelt profile                            |
| `oym-ai-jail update`             | Self-update to the latest GitHub release                       |
| `oym-ai-jail uninstall`          | Remove the zshrc wiring (`--all` also removes binary + config) |
| `oym-ai-jail version`            | Version info                                                   |

## Agent notes

- **Claude Code** gets read/write on `~/.claude`, `~/.claude.*` and
  `~/Library/Caches/claude-cli-nodejs`. Its installed binary
  (`~/.local/share/claude`, symlinked from `~/.local/bin/claude`) is
  deliberately **not** writable inside the jail, so the built-in updater
  can't run there. Update outside the jail with `command claude update`
  (`command` skips the wrapper) — keeping the binary sealed is what makes
  that escape hatch safe to run.
- **Mistral Vibe** gets `~/.vibe` and `~/.mistral`.
- **Codex** gets `~/.codex` (or `$CODEX_HOME`) and is launched with
  `--sandbox danger-full-access`: Codex applies its own Seatbelt
  profile on macOS and Seatbelt profiles do not nest — its `sandbox_apply` would fail with `Operation not permitted` inside our outer jail. The outer `oym-ai-jail` sandbox is the single, enforced boundary.

Sub-processes spawn normally inside the jail — re-jailing is skipped only after a
real Seatbelt probe confirms the process is already sandboxed, so a forged
`AI_JAIL_ACTIVE` variable alone does not bypass the jail.

## What it does not protect against

The jail is a **filesystem boundary**, not full process isolation. Be aware of
the honest threat model:

- **Network is unrestricted.** An agent can reach anything your machine can —
  exfiltrating whatever it can *read*. The jail limits what is readable, not
  where it can be sent.
- **Local IPC stays open** — that includes your `ssh-agent` socket (loaded keys
  are usable), and the Docker/OrbStack socket. Whoever talks to the Docker
  daemon effectively owns the host (it can mount your home dir into a
  container). If that trade-off is unacceptable, don't run agents that need
  Docker, or stop the daemon first.
- **Tools can ask macOS to act outside the jail** — `osascript`, `open`,
  `launchctl` and friends talk to system daemons that run unconfined. This is
  inherent to `sandbox-exec` (`allow default` for non-file operations) and is
  the same trade-off the agents' own sandboxes make.
- **The wrappers cover interactive zsh only.** `command claude`, an absolute
  path, a script, an IDE terminal, `bash`, or `zsh -c` bypass the wrappers —
  run `oym-ai-jail shell` if you need a jailed shell where the function
  doesn't apply. Each agent also gets write access only to *its own* config
  dirs, so a jailed Claude can't read `~/.vibe` or `~/.codex`.
- **Agent config files are writable — including their hooks.** Agents need
  `~/.claude`, `~/.codex`, `~/.vibe` writable, and those configs can carry
  hooks and MCP servers that execute at launch (`settings.json`,
  `~/.claude.json` `mcpServers`, `config.toml`, …). Sealing them would break
  normal agent features (`/model`, saved permissions, plugin installs) and
  `~/.claude.json` can't be sealed at all. A poisoned hook only fires when
  the agent runs *outside* the jail — so keep every launch going through the
  wrappers, and treat direct `command claude` / IDE-spawned sessions as
  trusted operations.
- **Some credentials are still reachable** — e.g. the login keychain file and
  agent tokens (`~/.claude` must stay writable for the agent to work).
  File-based secrets are outright unreadable (GPG private keys, cloud CLI
  credentials, VM ssh keys under `~/.lima`/`~/.colima`) — extend with
  `deny_read_paths`. Note the keychain caveat below.
  Package-manager caches (`~/.npm`, `~/.m2/repository`, `~/go/pkg`, cargo
  registry, gradle caches, …) are writable by necessity: downloaded
  dependency code executes at build time anyway, in and out of the jail. And
  a determined agent can still read whatever else the profile allows and
  send it out over the open network.
- **`gh`, `glab` and `docker` work inside the jail, and their tokens are
  reachable there.** On macOS these tools store credentials in the OS
  keychain, not in their config files — and the keychain is IPC to
  `securityd`, which Seatbelt file rules cannot mediate. Denying the config
  files broke the tools while protecting nothing, so they're not denied
  *unless* a plaintext credential is actually found in the config file
  (older versions, `--insecure-storage`, no keyring/`credsStore`) — then
  the deny applies and the tool stops working inside the jail. `oym-ai-jail
  status` warns when it detects this, with the re-login hint that moves the
  credential into the keychain. Either way: treat anything the keychain
  unlocks as in-scope for a jailed agent, same as the ssh-agent.

The practical takeaway: the jail is very good at "the agent can't break or
steal from places you haven't allowed" and mediocre at "the agent can't talk
to the outside world". Combine it with the agent's own permission prompts.

## Development

```bash
make build   # _output/oym-ai-jail
make test    # go vet + go test (includes real sandbox-exec integration tests)
```

Releases are SemVer tags; GoReleaser builds the darwin binaries. Users pick up changes with `oym-ai-jail update`, or pin a version from the releases page.
