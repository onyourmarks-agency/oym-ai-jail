// Package sandbox builds the Seatbelt (SBPL) profile for the current config
// and working directory, and launches processes under /usr/bin/sandbox-exec.
//
// Rule ordering matters: in SBPL the last matching rule wins. The profile
// therefore opens with a global deny, adds increasingly specific allows, and
// closes with a final deny that protects oym-ai-jail's own files — so even
// an agent with a writable home dir cannot rewrite the jail.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/agents"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/config"
)

// rwHomeSubpaths are directories under $HOME a jailed process may read and
// write. Anything on PATH or sourced by the shell stays read-only — an agent
// that can rewrite it would execute code outside the jail on the next run.
var rwHomeSubpaths = []string{
	"~/.oh-my-zsh/cache",
	"~/.oh-my-zsh/log",
	"~/.local/share",
	"~/.local/state",
	"~/.agents",
	"~/.cache",
	"~/.npm",
	"~/.composer/cache",
	"~/.ddev",
	"~/.docker",
	"~/.orbstack",
	"~/.cargo/registry",
	"~/.cargo/git",
	"~/.bun/install/cache",
	// Scoped to cache dirs: the parents also hold private keys, init
	// scripts and stored passwords that must stay unreachable.
	"~/.gradle/caches",
	"~/.gradle/wrapper",
	"~/.gradle/daemon",
	"~/.gradle/native",
	"~/.gradle/jdks",
	"~/.m2/repository",
	"~/.pub-cache",
	"~/.terraform.d/plugin-cache",
	"~/.colima",
	"~/.lima",
	"~/.android/avd",
	"~/.android/cache",
	"~/go/pkg",
	"~/Library/pnpm/store",
	"~/Library/pnpm/cache",
	"~/Library/Caches/go-build",
	"~/Library/Developer/Xcode",
	"~/Library/Developer/CoreSimulator",
	"~/Library/Keychains",
}

// rwHomeRegexes reopen the few paths inside read-only dirs that need write
// access: the gpg-agent sockets (git commit signing talks to the agent
// through them) and keyboxd's journal dir.
var rwHomeRegexes = []string{
	`^%HOME%/\.gnupg/S\..*`,
	`^%HOME%/\.gnupg/public-keys\.d.*`,
}

// roHomeRegexes cover sourced or tamper-prone files that must never be
// writable: zsh sources .zcompdump on startup, and a writable .zsh_history
// would let an agent plant commands in the user's history.
var roHomeRegexes = []string{
	`^%HOME%/\.zcompdump.*`,
	`^%HOME%/\.zsh_history.*`,
}

// poisonLiterals are files inside writable directories that would let an
// agent redirect tools the user runs unjailed: credential helpers, hooks,
var poisonLiterals = []string{
	"~/.docker/config.json",
}

// poisonSubpaths are directories inside writable dirs whose contents get
// executed or sourced outside the jail — either via PATH entries, shell init,
// or plugin discovery. They stay executable (readable) but never writable.
var poisonSubpaths = []string{
	"~/.orbstack/bin",                // on PATH
	"~/.orbstack/shell",              // sourced from ~/.zprofile
	"~/.local/share/claude",          // ~/.local/bin/claude symlinks into versions/
	"~/.local/share/uv/tools",        // uv tool shims on PATH
	"~/.local/share/mise",            // mise tool installs on PATH
	"~/.docker/cli-plugins",          // `docker compose`/`buildx` executables
	"~/.ddev/bin",                    // ddev-downloaded tools, e.g. docker-compose
	"~/.ddev/commands",               // custom `ddev <cmd>` shell scripts
	"~/.ddev/homeadditions",          // mounted+sourced inside ddev containers
	"~/.oh-my-zsh/cache/completions", // in fpath — completions get sourced
	"~/.local/share/pnpm",            // pnpm global bin dir
	"~/.local/share/pipx",            // pipx venvs, shimmed into ~/.local/bin
	"~/.pub-cache/bin",               // `dart pub global` shims
}

// denyReadDefaults may never even be read inside the jail: credentials whose
// theft beats a missing feature (GPG signing still works through the agent
// socket). Users extend this list with deny_read_paths in config.yaml — the
// baseline always applies.
var denyReadDefaults = []string{
	"~/.config/hub",    // legacy GitHub CLI — always plaintext tokens
	"~/.config/gcloud", // GCP credentials + access tokens
	"~/.config/rclone", // rclone.conf holds every remote's secret
	"~/.config/doctl",  // DigitalOcean
	"~/.config/hcloud", // Hetzner
	"~/.config/linode-cli",
	"~/.config/helm",                 // registry auth
	"~/.config/containers/auth.json", // podman registry creds
	"~/.config/configstore",          // firebase, gulp and friends
	"~/.config/stripe",
	"~/.config/twilio",
	"~/.composer/auth.json", // Packagist/GitHub tokens
	"~/.config/filezilla",   // stored FTP passwords
	"~/.config/github-copilot",
	"~/.config/sentry",
	"~/.config/ddev-secrets",
	"~/.config/op",                         // 1Password CLI
	"~/.terraform.d/credentials.tfrc.json", // terraform registry tokens
	"~/.colima/_lima",                      // per-VM ssh keys (colima runs on lima)
	"~/.lima/_config",                      // lima ssh keys + network config
	"~/.gnupg/private-keys-v1.d",           // OpenPGP private key material
	"~/.gnupg/secring.gpg",                 // legacy private keyring
}

// conditionalTokenFiles are inspected while building the profile. gh, glab
// and docker store credentials in the OS keychain by default on macOS; their
// config files then carry no secrets, so denying them would break the tools
// while protecting nothing — the keychain itself is beyond Seatbelt's reach
// either way (IPC to securityd, not file access). When a plaintext token IS
// present (older versions, --insecure-storage, no keyring/credsStore) the
// file is a real credential: the deny applies and the tool stops working
// inside the jail.
var conditionalTokenFiles = []struct {
	tool    string // display name for warnings
	file    string // config file to inspect
	dir     string // config dir to deny or open read-only
	roAllow bool   // whether a clean dir needs an explicit ro rule (not yet covered)
	hint    string // remediation shown in `oym-ai-jail status`
}{
	{"gh", "~/.config/gh/hosts.yml", "~/.config/gh", false,
		"run `gh auth login` again to move the token into the macOS keychain"},
	{"glab", "~/.config/glab-cli/config.yml", "~/.config/glab-cli", false,
		"run `glab auth login` again to move the token into the macOS keychain"},
	{"glab", "~/Library/Application Support/glab-cli/config.yml", "~/Library/Application Support/glab-cli", true,
		"run `glab auth login` again to move the token into the macOS keychain"},
	{"docker", "~/.docker/config.json", "~/.docker/config.json", false,
		`enable a credential helper ("credsStore": "osxkeychain") and run docker login again`},
}

// tokenSecretRe matches non-empty inline token values in gh/glab YAML.
// Horizontal whitespace only — \s would eat the newline after an empty
// "token:" and match the next line's first character as a "value".
var tokenSecretRe = regexp.MustCompile(`(?m)^[ \t]*(oauth_token|token|job_token):[ \t]*\S`)

// dockerPlaintextAuthRe matches a non-empty "auth" entry in docker's JSON
// config — base64-encoded user:password for a registry.
var dockerPlaintextAuthRe = regexp.MustCompile(`"auth"\s*:\s*"[^"]+"`)

// PlaintextTokenRisk describes one tool whose config currently holds a
// plaintext credential that the jail will seal off.
type PlaintextTokenRisk struct {
	Tool string
	File string
	Hint string
}

// DetectPlaintextTokens inspects every conditionalTokenFiles entry and
// reports which hold plaintext secrets. Runs outside the sandbox while
// building the profile, so the files are readable here.
func DetectPlaintextTokens(home string) []PlaintextTokenRisk {
	var out []PlaintextTokenRisk
	for _, c := range conditionalTokenFiles {
		full := config.Canonical(c.file, home)
		data, err := os.ReadFile(full)
		if err != nil {
			continue // not installed or not configured — nothing to do
		}
		matched := tokenSecretRe.Match(data)
		if c.tool == "docker" {
			matched = dockerPlaintextAuthRe.Match(data)
		}
		if matched {
			out = append(out, PlaintextTokenRisk{Tool: c.tool, File: full, Hint: c.hint})
		}
	}
	return out
}

// classifyTokenDirs splits conditionalTokenFiles into dirs holding plaintext
// secrets (deny) and clean dirs the tools need readable (ro).
func classifyTokenDirs(home string) (deny, ro []string) {
	risks := map[string]bool{}
	for _, r := range DetectPlaintextTokens(home) {
		risks[r.File] = true
	}
	for _, c := range conditionalTokenFiles {
		full := config.Canonical(c.file, home)
		if _, err := os.Stat(full); err != nil {
			continue
		}
		if risks[full] {
			deny = append(deny, config.Canonical(c.dir, home))
		} else if c.roAllow {
			ro = append(ro, config.Canonical(c.dir, home))
		}
	}
	return deny, ro
}

// devLiterals are the pseudo devices a jailed process may write — the rest
// of /dev stays readable (allow default) but unwritable, so disks and
// hardware can't be modified.
var devLiterals = []string{
	"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom",
	"/dev/tty", "/dev/ptmx", "/dev/stdin", "/dev/stdout", "/dev/stderr",
}

// devRegexes cover numbered pseudo terminals and the fd symlinks.
var devRegexes = []string{
	`^/dev/ttys[0-9]+$`,
	`^/dev/fd/[0-9]+$`,
}

// roHomeLiterals are dotfiles agents may read but never modify.
var roHomeLiterals = []string{
	"~/.zshrc",
	"~/.zprofile",
	"~/.zshenv",
	"~/.zlogin",
	"~/.zlogout",
	"~/.profile",
	"~/.bash_profile",
	"~/.bashrc",
	"~/.gitconfig",
	"~/.gitignore",
	"~/.gitignore_global",
	"~/.tmux.conf",
	"~/.vimrc",
	"~/.emacs",
}

// roHomeSubpaths are tool directories used read-only inside the jail.
// Executables and sourced config live here: readable so tools can run,
// never writable so the agent cannot poison what the user runs later.
var roHomeSubpaths = []string{
	"~/.oh-my-zsh",
	"~/.nvm",
	"~/.asdf",
	"~/.pyenv",
	"~/.rbenv",
	"~/.rvm",
	"~/.jenv",
	"~/.nodenv",
	"~/.goenv",
	"~/.phpenv",
	"~/.tfenv",
	"~/.gvm",
	"~/.sdkman",
	"~/.volta",
	"~/.deno",
	"~/.nix-profile",
	"~/.local",
	"~/.config",
	"~/.composer",
	"~/.cargo",
	"~/.rustup",
	"~/.bun",
	"~/.yarn",
	"~/.gnupg",
	"~/.zsh_sessions", // session-restore files get sourced by new terminals
	"~/.gem",
	"~/.bundle",
	"~/.tmux",
	"~/.vim",
	"~/.emacs.d",
	"~/.doom.d",
	"~/.antigen",
	"~/.zinit",
	"~/.zim",
	"~/.zprezto",
	"~/.zplug",
	"~/.krew",
	"~/.rd", // Rancher Desktop bin/
	"~/bin",
	"~/go",
	"~/Library/pnpm",
	"~/Library/Application Support/fnm",
	"~/Library/Caches/fnm_multishells", // per-shell PATH entries created by `fnm env`
}

// protectedLiterals can never be written from inside the jail, even when a
// broader write rule would match them.
func protectedLiterals(home, selfPath, legacySandboxPath string) []string {
	literals := []string{
		legacySandboxPath, // ~/.ai-sandbox.sb from the manual setup
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".zprofile"),
		filepath.Join(home, ".zshenv"),
		filepath.Join(home, ".zlogin"),
		filepath.Join(home, ".zlogout"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".bashrc"),
	}
	if selfPath != "" {
		literals = append(literals, selfPath)
	}
	return literals
}

// escape quotes a string for use inside an SBPL string literal.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

type filter struct {
	kind string // "subpath", "literal" or "regex"
	val  string
}

func subpaths(paths []string) []filter {
	out := make([]filter, 0, len(paths))

	for _, p := range paths {
		out = append(out, filter{kind: "subpath", val: p})
	}

	return out
}

func literals(paths []string) []filter {
	out := make([]filter, 0, len(paths))

	for _, p := range paths {
		out = append(out, filter{kind: "literal", val: p})
	}

	return out
}

func regexes(patterns []string) []filter {
	out := make([]filter, 0, len(patterns))

	for _, p := range patterns {
		out = append(out, filter{kind: "regex", val: p})
	}

	return out
}

func dedupe(fs []filter) []filter {
	seen := map[string]bool{}
	out := fs[:0]

	for _, f := range fs {
		key := f.kind + "\x00" + f.val
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}

	return out
}

func rule(b *strings.Builder, ops string, fs []filter) {
	if len(fs) == 0 {
		return
	}

	b.WriteString("(" + ops)

	for _, f := range fs {
		fmt.Fprintf(b, "\n  (%s %s)", f.kind, escape(f.val))
	}

	b.WriteString(")\n")
}

// ancestors collects every parent directory of the given paths (excluding
// "/" and the paths themselves) so tools can stat their way down to allowed
// locations without gaining read access to sibling directories.
func ancestors(paths []string) []filter {
	seen := map[string]bool{}

	for _, p := range paths {
		for dir := filepath.Dir(p); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
			if seen[dir] {
				break
			}
			seen[dir] = true
		}
	}

	out := make([]string, 0, len(seen))

	for dir := range seen {
		out = append(out, dir)
	}

	sort.Strings(out)
	return literals(out)
}

// Build renders the SBPL profile for cfg in working directory cwd.
// selfPath is the resolved oym-ai-jail binary, protected from jail writes.
func Build(cfg *config.Resolved, enabled []agents.Agent, cwd, selfPath string) string {
	home := cfg.Home
	project, ws, inWorkspace, atWorkspaceRoot := cfg.Project(cwd)

	var agentSub, agentRe []string

	for _, a := range enabled {
		rule := a.Access(home)
		agentSub = append(agentSub, rule.RWSubpaths...)
		agentRe = append(agentRe, rule.RWRegexes...)
	}

	homeRegex := func(patterns []string) []string {
		out := make([]string, len(patterns))
		for i, p := range patterns {
			out[i] = regexp.MustCompile(`%HOME%`).ReplaceAllString(p, regexp.QuoteMeta(home))
		}

		return out
	}
	canonHome := func(paths []string) []string {
		out := make([]string, len(paths))
		for i, p := range paths {
			out[i] = config.Canonical(p, home)
		}

		return out
	}

	rwToolchain := canonHome(rwHomeSubpaths)
	roLiterals := canonHome(roHomeLiterals)
	roSubpaths := canonHome(roHomeSubpaths)

	// Protect the real config location, including when it is overridden with
	// OYM_AI_JAIL_CONFIG — an agent must not rewrite its own jail.
	configPath := cfg.ConfigPath
	if configPath == "" {
		configPath = "~/.config/oym-ai-jail/config.yaml"
	}

	configPath = config.Canonical(configPath, home)
	configDir := filepath.Dir(configPath)
	// Canonicalise the protected list too: ~/.zshrc may itself be a symlink
	// (dotfiles repo) and Seatbelt only ever sees the real path.
	var protected []string

	for _, p := range protectedLiterals(home, selfPath, config.Canonical("~/.ai-sandbox.sb", home)) {
		protected = append(protected, config.Canonical(p, home))
	}

	protected = append(protected, configPath)

	for _, p := range poisonLiterals {
		protected = append(protected, config.Canonical(p, home))
	}

	poisonSub := canonHome(poisonSubpaths)
	denyRead := append(canonHome(denyReadDefaults), cfg.DenyReadPaths...)
	tokenDeny, tokenRO := classifyTokenDirs(home)
	denyRead = append(denyRead, tokenDeny...)

	// Deny writes to every ancestor of a protected path between it and home:
	// renaming a writable parent and symlinking a lookalike back would move
	// the real dir out from under its deny rules (Seatbelt sees real paths).
	var sealed []string

	for _, p := range append(append(append([]string{}, poisonSub...), denyRead...), protected...) {
		for dir := filepath.Dir(p); config.Within(home, dir) && dir != home; dir = filepath.Dir(dir) {
			sealed = append(sealed, dir)
		}
	}

	// Everything a rule touches needs traversable ancestors.
	var allPaths []string
	allPaths = append(allPaths, cfg.Workspaces...)
	allPaths = append(allPaths, cfg.ReadPaths...)
	allPaths = append(allPaths, cfg.WritePaths...)

	if inWorkspace {
		allPaths = append(allPaths, project)
	}

	allPaths = append(allPaths, agentSub...)
	allPaths = append(allPaths, rwToolchain...)
	allPaths = append(allPaths, roLiterals...)
	allPaths = append(allPaths, roSubpaths...)
	allPaths = append(allPaths, tokenRO...)
	allPaths = append(allPaths, configDir)

	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n\n")

	b.WriteString(";; 1. Deny writes everywhere; deny read+write in user data.\n")
	b.WriteString("(deny file-write*)\n")
	rule(&b, "deny file-read* file-write*", []filter{
		{kind: "subpath", val: "/Users"},
		{kind: "subpath", val: "/Volumes"},
	})

	b.WriteString("\n;; 2. Metadata-only access so pwd/git can traverse to allowed paths.\n")
	meta := append([]filter{{kind: "subpath", val: home}, {kind: "literal", val: "/Users"}, {kind: "literal", val: "/Volumes"}}, ancestors(allPaths)...)
	rule(&b, "allow file-read-metadata", dedupe(meta))

	b.WriteString("\n;; 3. System temp dirs and pseudo devices.\n")
	rule(&b, "allow file-read* file-write*", subpaths([]string{"/private/tmp", "/private/var/tmp", "/private/var/folders"}))
	rule(&b, "allow file-read-metadata", literals([]string{"/dev"}))
	rule(&b, "allow file-read* file-write*", dedupe(append(literals(devLiterals), regexes(devRegexes)...)))

	b.WriteString("\n;; 4. Read-only dotfiles, tool dirs and sourced files (before write\n;;    rules so the more specific write grants on their subdirs still win).\n")
	ro := append(literals(roLiterals), subpaths(roSubpaths)...)
	ro = append(ro, subpaths(tokenRO)...)
	rule(&b, "allow file-read*", dedupe(append(ro, regexes(homeRegex(roHomeRegexes))...)))

	b.WriteString("\n;; 5. Workspace visibility.\n")
	switch {
	case cfg.AllowWorkspaceWrite:
		rule(&b, "allow file-read* file-write*", subpaths(cfg.Workspaces))
	case cfg.AllowWorkspaceRead:
		rule(&b, "allow file-read*", subpaths(cfg.Workspaces))
	case atWorkspaceRoot:
		// Strict mode with cwd == the workspace root itself: at least let
		// the agent list the directory it sits in.
		rule(&b, "allow file-read*", literals([]string{ws}))
	}
	rule(&b, "allow file-read*", subpaths(cfg.ReadPaths))

	b.WriteString("\n;; 6. Write access: current project, explicit write paths, agents, toolchain.\n")
	var rw []filter

	switch {
	case inWorkspace && atWorkspaceRoot && !cfg.AllowWorkspaceWrite:
		// cwd IS the workspace root: a subpath grant would expose every
		// sibling project, so only direct children of the root are writable,
		// and the final deny below seals existing project dirs against
		// rename/delete. In write mode the whole workspace is open anyway.
		rw = append(rw, filter{kind: "regex", val: `^` + regexp.QuoteMeta(ws) + `/[^/]+$`})
	case inWorkspace:
		rw = append(rw, filter{kind: "subpath", val: project})
	}
	rw = append(rw, subpaths(cfg.WritePaths)...)
	rw = append(rw, subpaths(agentSub)...)
	rw = append(rw, subpaths(rwToolchain)...)
	rule(&b, "allow file-read* file-write*", dedupe(rw))
	rule(&b, "allow file-read* file-write*", dedupe(append(regexes(agentRe), regexes(homeRegex(rwHomeRegexes))...)))

	b.WriteString("\n;; 7. Read-only stays read-only, executable dirs cannot be poisoned,\n;;    and the jail itself is untouchable (last match wins).\n")
	finalDeny := append(subpaths(cfg.ReadPaths), filter{kind: "subpath", val: configDir})
	// At the workspace root, seal every existing project dir against
	// rename/delete — the direct-children regex above must not touch them.
	if inWorkspace && atWorkspaceRoot && !cfg.AllowWorkspaceWrite {
		if entries, err := os.ReadDir(ws); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					finalDeny = append(finalDeny, filter{kind: "subpath", val: filepath.Join(ws, e.Name())})
				}
			}
		}
	}
	finalDeny = append(finalDeny, subpaths(poisonSub)...)
	finalDeny = append(finalDeny, literals(protected)...)
	finalDeny = append(finalDeny, literals(sealed)...)
	// Deny writes to the directory holding our binary, not just the file,
	// so it cannot be renamed away and replaced with a fake oym-ai-jail.
	if selfPath != "" {
		finalDeny = append(finalDeny, filter{kind: "literal", val: filepath.Dir(selfPath)})
	}
	rule(&b, "deny file-write*", dedupe(finalDeny))

	b.WriteString("\n;; 8. Credentials that must not even be read inside the jail.\n")
	rule(&b, "deny file-read* file-write*", dedupe(subpaths(denyRead)))

	return b.String()
}
