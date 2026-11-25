package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/agents"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/config"
)

// The integration tests need a directory under /Users (the only subtree
// the profile denies by default), so they create one inside the real home.
func testBase(t *testing.T) (home, base string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("Seatbelt profiles only apply on macOS")
	}
	if _, err := os.Stat(SandboxExec); err != nil {
		t.Skip("sandbox-exec not available")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	if !strings.HasPrefix(home, "/Users/") {
		t.Skipf("home %q is not under /Users — deny rules would not apply", home)
	}
	base, err = os.MkdirTemp(home, ".oym-jail-test-")
	if err != nil {
		// Running the tests from inside the jail (e.g. `oym-ai-jail shell`)
		// denies writes to home — skip instead of failing.
		t.Skipf("cannot create test dir in home (jailed?): %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	return home, base
}

// mkTree builds root/{ws/{klant-a,klant-b},ro} — ro mirrors ~/ai-allowed
// sitting next to the workspaces instead of inside them.
func mkTree(t *testing.T, root string) (ws, klantA, klantB, ro string) {
	t.Helper()
	ws = filepath.Join(root, "ws")
	klantA = filepath.Join(ws, "klant-a")
	klantB = filepath.Join(ws, "klant-b")
	ro = filepath.Join(root, "shared-readonly")
	for _, dir := range []string{klantA, klantB, ro} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{
		filepath.Join(klantB, "secret.txt"),
		filepath.Join(ro, "note.txt"),
	} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws, klantA, klantB, ro
}

// jailed runs script inside the generated profile and reports whether it
// succeeded.
func jailed(t *testing.T, profile, script string) bool {
	t.Helper()
	cmd := exec.Command(SandboxExec, "-p", profile, "/bin/sh", "-c", script)
	return cmd.Run() == nil
}

func resolvedCfg(home, base, ro string, read, write bool) *config.Resolved {
	return &config.Resolved{
		Home:                home,
		Workspaces:          []string{base},
		AllowWorkspaceRead:  read,
		AllowWorkspaceWrite: write,
		ReadPaths:           []string{ro},
		Agents:              map[string]bool{},
	}
}

func TestProfileModes(t *testing.T) {
	home, root := testBase(t)
	ws, klantA, klantB, ro := mkTree(t, root)
	self := filepath.Join(klantA, "fake-oym-ai-jail")
	if err := os.WriteFile(self, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		read, write bool
		script      string
		wantAllowed bool
	}{
		// strict: workspace invisible
		{"strict reads sibling", false, false, "cat " + klantB + "/secret.txt", false},
		{"strict writes sibling", false, false, "touch " + klantB + "/x", false},
		{"strict lists sibling", false, false, "ls " + klantB, false},
		{"strict writes cwd", false, false, "touch " + klantA + "/x", true},
		{"strict reads read_paths", false, false, "cat " + ro + "/note.txt", true},
		{"strict writes read_paths", false, false, "touch " + ro + "/x", false},
		{"strict lists home", false, false, "ls " + home, false},
		{"strict keeps pwd", false, false, "pwd", true},

		// read: workspace readable, cwd writable
		{"read reads sibling", true, false, "cat " + klantB + "/secret.txt", true},
		{"read writes sibling", true, false, "touch " + klantB + "/x", false},
		{"read writes cwd", true, false, "touch " + klantA + "/x", true},
		{"read writes read_paths", true, false, "touch " + ro + "/x", false},

		// write: whole workspace writable
		{"write writes sibling", true, true, "touch " + klantB + "/x", true},
		{"write reads sibling", true, true, "cat " + klantB + "/secret.txt", true},
		{"write still ro read_paths", true, true, "touch " + ro + "/x", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := resolvedCfg(home, ws, ro, tc.read, tc.write)
			profile := Build(rc, nil, klantA, self)
			if got := jailed(t, profile, tc.script); got != tc.wantAllowed {
				t.Fatalf("script %q allowed=%v want %v", tc.script, got, tc.wantAllowed)
			}
		})
	}
}

func TestProfileSelfProtection(t *testing.T) {
	home, root := testBase(t)
	ws, klantA, _, ro := mkTree(t, root)
	self := filepath.Join(klantA, "fake-oym-ai-jail")
	if err := os.WriteFile(self, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Even in full write mode and sitting in the writable cwd, the binary
	// itself stays untouchable.
	rc := resolvedCfg(home, ws, ro, true, true)
	profile := Build(rc, nil, klantA, self)
	if jailed(t, profile, "echo x >> "+self) {
		t.Fatal("jailed process could overwrite the oym-ai-jail binary")
	}
	// touch is a file-write* op too — denied identically, harmless if a
	// regression ever let it through.
	if jailed(t, profile, "touch "+filepath.Join(home, ".zshrc")) {
		t.Fatal("jailed process could write ~/.zshrc")
	}
	// CI runners have no ~/.zshrc — create a stub so the read check tests
	// the profile, not the file's absence.
	zshrc := filepath.Join(home, ".zshrc")
	if _, err := os.Stat(zshrc); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(zshrc, []byte("# test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !jailed(t, profile, "cat "+zshrc) {
		t.Fatal("~/.zshrc must stay readable inside the jail")
	}
}

// Starting an agent in the workspace root itself must not hand write access
// to every project inside: only files directly in the root stay writable.
func TestProfileWorkspaceRoot(t *testing.T) {
	home, root := testBase(t)
	ws, _, klantB, ro := mkTree(t, root)

	profile := Build(resolvedCfg(home, ws, ro, false, false), nil, ws, "")
	if jailed(t, profile, "touch "+klantB+"/x") {
		t.Fatal("workspace-root cwd could write into a sibling project")
	}
	if jailed(t, profile, "mv "+klantB+" "+klantB+"-moved") {
		t.Fatal("workspace-root cwd could rename a sibling project away")
	}
	if !jailed(t, profile, "touch "+ws+"/loose-file") {
		t.Fatal("workspace-root cwd should still write files at the root")
	}
	if !jailed(t, profile, "ls "+ws+" >/dev/null") {
		t.Fatal("workspace-root cwd should be able to list itself")
	}

	// In write mode the whole workspace is open anyway — no restriction.
	profile = Build(resolvedCfg(home, ws, ro, true, true), nil, ws, "")
	if !jailed(t, profile, "touch "+klantB+"/x") {
		t.Fatal("write mode should allow sibling writes from the root")
	}
}

// A protected dir inside a writable parent must not be escapable by
// renaming the parent and symlinking a lookalike back — Seatbelt matches
// real paths, so the parent dir itself gets a literal write-deny.
func TestProfilePoisonParentSealed(t *testing.T) {
	home, base := testBase(t)
	parent := filepath.Join(base, "poison-parent")
	protected := filepath.Join(parent, "bin")
	if err := os.MkdirAll(protected, 0o755); err != nil {
		t.Fatal(err)
	}
	// The rename destination must itself be writable — otherwise mv fails
	// regardless of the seal and the test proves nothing.
	dest := filepath.Join(base, "writable")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	old := poisonSubpaths
	poisonSubpaths = append(poisonSubpaths, protected)
	defer func() { poisonSubpaths = old }()

	rc := &config.Resolved{
		Home:       home,
		Agents:     map[string]bool{},
		WritePaths: []string{parent, dest},
	}
	profile := Build(rc, nil, filepath.Join(home, "elsewhere"), "")

	if !jailed(t, profile, "touch "+parent+"/other") {
		t.Fatal("writes inside the writable parent should be allowed")
	}

	if jailed(t, profile, "mv "+parent+" "+dest+"/moved") {
		t.Fatal("could rename the parent out from under the protected dir")
	}

	if jailed(t, profile, "touch "+protected+"/x") {
		t.Fatal("protected dir is writable")
	}
}

// deny_read_paths hides credentials entirely — not even readable.
func TestProfileDenyReadPaths(t *testing.T) {
	home, base := testBase(t)
	secret := filepath.Join(base, "secret")

	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(secret, "token"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := &config.Resolved{
		Home:          home,
		Agents:        map[string]bool{},
		ReadPaths:     []string{base}, // readable parent — the deny must win
		DenyReadPaths: []string{secret},
	}
	profile := Build(rc, nil, filepath.Join(home, "elsewhere"), "")

	if jailed(t, profile, "cat "+secret+"/token") {
		t.Fatal("deny_read_paths target is readable")
	}

	if jailed(t, profile, "touch "+secret+"/x") {
		t.Fatal("deny_read_paths target is writable")
	}

	if !jailed(t, profile, "ls "+base+" >/dev/null") {
		t.Fatal("readable parent should still work")
	}
}

func TestProfileContent(t *testing.T) {
	rc := &config.Resolved{
		Home:                "/Users/jane",
		Workspaces:          []string{"/Users/jane/dev"},
		AllowWorkspaceRead:  true,
		AllowWorkspaceWrite: false,
		ReadPaths:           []string{"/Users/jane/ai-allowed"},
		Agents:              map[string]bool{"claude": true},
	}
	profile := Build(rc, testAgents(), "/Users/jane/dev/x", "/usr/local/bin/oym-ai-jail")

	for _, want := range []string{
		"(deny file-write*)",
		`(subpath "/Users")`,
		`(subpath "/Users/jane/dev")`,
		`(subpath "/Users/jane/dev/x")`,   // cwd writable
		`(subpath "/Users/jane/.claude")`, // agent rw
		`Library/Caches/claude-cli-nodejs`,
		`Library/Application Support/fnm`, // toolchain incl. spaced paths
		`deny file-write*`,
		`oym-ai-jail`,
		`.zshrc`,
		`/Users/jane/.gnupg/private-keys-v1.d`, // gpg key material
		`/Users/jane/.docker/cli-plugins`,      // poison dir
		`/Users/jane/.local/share/claude`,      // symlinked agent binary
	} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile missing %q:\n%s", want, profile)
		}
	}
	// The self-protection deny must come after every allow.
	lastDeny := strings.LastIndex(profile, "(deny file-write*")
	lastAllow := strings.LastIndex(profile, "(allow")

	if lastDeny < lastAllow {
		t.Fatalf("self-protection deny is not last:\n%s", profile)
	}
}

// gh/glab tokens normally live in the macOS keychain, so their config dirs
// stay readable — denying them would break the tools while protecting
// nothing. Only when a plaintext token is found in the file itself does the
// deny apply.
func TestPlaintextTokenDenies(t *testing.T) {
	home := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Keychain-backed: hosts.yml holds metadata only — no deny.
	write(".config/gh/hosts.yml", "github.com:\n    user: jane\n    git_protocol: ssh\n")
	if deny, _ := classifyTokenDirs(home); len(deny) != 0 {
		t.Fatalf("keychain-only setup should yield no denies, got %v", deny)
	}

	// Plaintext storage (--insecure-storage or old gh) — deny kicks in.
	write(".config/gh/hosts.yml", "github.com:\n    user: jane\n    oauth_token: gho_secret\n")
	deny, _ := classifyTokenDirs(home)
	if len(deny) != 1 || !strings.HasSuffix(deny[0], ".config/gh") {
		t.Fatalf("plaintext token should yield gh deny, got %v", deny)
	}

	// glab with inline token is detected independently; empty values are not secrets.
	write("Library/Application Support/glab-cli/config.yml", "hosts:\n  gitlab.com:\n    job_token:\n")
	if deny, _ := classifyTokenDirs(home); len(deny) != 1 {
		t.Fatalf("empty job_token is not a secret, got %v", deny)
	}
	write("Library/Application Support/glab-cli/config.yml", "hosts:\n  gitlab.com:\n    token: glpat-secret\n")
	deny, _ = classifyTokenDirs(home)
	if len(deny) != 2 {
		t.Fatalf("expected gh + glab denies, got %v", deny)
	}

	// A clean glab dir is opened read-only instead, so the tool still works.
	write(".config/gh/hosts.yml", "github.com:\n    user: jane\n")
	write("Library/Application Support/glab-cli/config.yml", "hosts:\n  gitlab.com:\n    user: jane\n")
	deny, ro := classifyTokenDirs(home)
	if len(deny) != 0 || len(ro) != 1 || !strings.HasSuffix(ro[0], "glab-cli") {
		t.Fatalf("clean setup: deny=%v ro=%v", deny, ro)
	}

	// docker: keychain-backed credsStore is fine, plaintext auth is not.
	write(".docker/config.json", `{"credsStore":"osxkeychain","auths":{}}`)
	if deny, _ := classifyTokenDirs(home); len(deny) != 0 {
		t.Fatalf("credsStore-only docker config should be fine, got %v", deny)
	}
	write(".docker/config.json", `{"auths":{"https://index.docker.io/v1/":{"auth":"dXNlcjpwYXNz"}}}`)
	if deny, _ := classifyTokenDirs(home); len(deny) != 1 {
		t.Fatalf("plaintext docker auth should be denied, got %v", deny)
	}
	// An empty auth value is not a secret.
	write(".docker/config.json", `{"auths":{"https://index.docker.io/v1/":{"auth":""}}}`)
	if deny, _ := classifyTokenDirs(home); len(deny) != 0 {
		t.Fatalf("empty docker auth is not a secret, got %v", deny)
	}
}

func testAgents() []agents.Agent {
	return []agents.Agent{{
		Name: "claude",
		RW: agents.Rule{
			RWSubpaths: []string{"~/.claude", "~/Library/Caches/claude-cli-nodejs"},
			RWRegexes:  []string{`^%HOME%/\.claude\..*`},
		},
	}}
}
