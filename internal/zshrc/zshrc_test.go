package zshrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const legacySnippet = `
some existing zshrc content

# ============================================================================
# AUTOMATISCHE JAIL-WRAPPER VOOR CLAUDE & VIBE
# ============================================================================
claude() {
  if [[ "$AI_JAIL_ACTIVE" == "1" ]]; then
    command claude "$@"
  else
    /usr/local/bin/ai-shell -c "claude $(printf '%q ' "$@")"
  fi
}

vibe() {
  if [[ "$AI_JAIL_ACTIVE" == "1" ]]; then
    command vibe "$@"
  else
    /usr/local/bin/ai-shell -c "vibe $(printf '%q ' "$@")"
  fi
}
`

func writeRc(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".zshrc")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readRc(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEnsureAppendsBlock(t *testing.T) {
	path := writeRc(t, "export EDITOR=vim\n")
	changed, err := Ensure(path)

	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}

	content := readRc(t, path)

	if !strings.Contains(content, Begin) || !strings.Contains(content, "oym-ai-jail shell-init") {
		t.Fatalf("block missing:\n%s", content)
	}

	if !strings.HasPrefix(content, "export EDITOR=vim") {
		t.Fatalf("existing content lost:\n%s", content)
	}
}

func TestEnsureIdempotent(t *testing.T) {
	path := writeRc(t, "")

	if _, err := Ensure(path); err != nil {
		t.Fatal(err)
	}

	changed, err := Ensure(path)

	if err != nil || changed {
		t.Fatalf("second Ensure should be a no-op, changed=%v err=%v", changed, err)
	}
}

func TestRemoveBlock(t *testing.T) {
	path := writeRc(t, "before\n")

	if _, err := Ensure(path); err != nil {
		t.Fatal(err)
	}

	removed, err := Remove(path)

	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}

	if strings.Contains(readRc(t, path), "oym-ai-jail") {
		t.Fatal("block still present")
	}
}

func TestRemoveLegacy(t *testing.T) {
	path := writeRc(t, legacySnippet)
	has, err := HasLegacy(path)

	if err != nil || !has {
		t.Fatalf("HasLegacy=%v err=%v", has, err)
	}

	changed, err := RemoveLegacy(path)

	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}

	content := readRc(t, path)
	if strings.Contains(content, "ai-shell") || strings.Contains(content, "JAIL-WRAPPER") {
		t.Fatalf("legacy still present:\n%s", content)
	}

	if !strings.Contains(content, "some existing zshrc content") {
		t.Fatalf("unrelated content lost:\n%s", content)
	}

	if strings.Contains(content, "=====") {
		t.Fatalf("banner left over:\n%s", content)
	}
}

// One-line function definitions must be removed without eating the lines
// after them — the original brace scan ran past EOF and mangled the file.
func TestRemoveLegacyOneLiner(t *testing.T) {
	path := writeRc(t, `export EDITOR=vim
claude() { ai-shell claude "$@" }
export PATH=/usr/local/bin:$PATH
other() { echo hi }
`)
	changed, err := RemoveLegacy(path)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}

	content := readRc(t, path)

	for _, want := range []string{"export EDITOR=vim", "export PATH=", "other() { echo hi }"} {
		if !strings.Contains(content, want) {
			t.Fatalf("lost %q:\n%s", want, content)
		}
	}

	if strings.Contains(content, "ai-shell") {
		t.Fatalf("legacy still present:\n%s", content)
	}
}

// A one-line legacy function at EOF has no closing "}" on its own line —
// removing it must not panic.
func TestRemoveLegacyOneLinerAtEOF(t *testing.T) {
	path := writeRc(t, "export EDITOR=vim\nclaude() { ai-shell claude \"$@\" }\n")

	if _, err := RemoveLegacy(path); err != nil {
		t.Fatalf("RemoveLegacy: %v", err)
	}

	if strings.Contains(readRc(t, path), "ai-shell") {
		t.Fatal("legacy still present")
	}
}

// An unterminated function must be left alone rather than deleting to EOF.
func TestRemoveLegacyUnterminated(t *testing.T) {
	src := "export A=1\nclaude() {\n  ai-shell claude \"$@\"\nexport B=2\n"
	path := writeRc(t, src)

	if _, err := RemoveLegacy(path); err != nil {
		t.Fatal(err)
	}

	if readRc(t, path) != src {
		t.Fatalf("file changed:\n%s", readRc(t, path))
	}
}

// Every write must leave a backup of the previous file.
func TestWriteBackup(t *testing.T) {
	path := writeRc(t, "export EDITOR=vim\n")

	if _, err := Ensure(path); err != nil {
		t.Fatal(err)
	}

	if got := readRc(t, BackupPath(path)); got != "export EDITOR=vim\n" {
		t.Fatalf("backup = %q", got)
	}
}

// A block with the begin marker but no end marker is corrupt: Ensure must
// strip the stray marker and append a fresh block, not report "wired".
func TestEnsureRepairsPartialBlock(t *testing.T) {
	path := writeRc(t, "export EDITOR=vim\n"+Begin+"\neval \"$(oym-ai-jail shell-init)\"\n")
	changed, err := Ensure(path)

	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}

	content := readRc(t, path)

	if strings.Count(content, Begin) != 1 || strings.Count(content, End) != 1 {
		t.Fatalf("block not repaired:\n%s", content)
	}

	if strings.Count(content, "oym-ai-jail shell-init") != 1 {
		t.Fatalf("stale eval line left behind — shell-init would run twice:\n%s", content)
	}

	if !strings.Contains(content, "export EDITOR=vim") {
		t.Fatalf("existing content lost:\n%s", content)
	}
}

// A second write must not clobber the backup of the original file —
// RemoveLegacy + Ensure run back to back during migration.
func TestWriteBackupNotOverwritten(t *testing.T) {
	path := writeRc(t, "export EDITOR=vim\nclaude() { ai-shell claude \"$@\" }\n")

	if _, err := RemoveLegacy(path); err != nil {
		t.Fatal(err)
	}

	if _, err := Ensure(path); err != nil {
		t.Fatal(err)
	}

	backup := readRc(t, BackupPath(path))

	if !strings.Contains(backup, "ai-shell") {
		t.Fatalf("backup lost the pre-migration content:\n%s", backup)
	}

	// The rolling backup holds the state just before the latest write —
	// here the file after RemoveLegacy ran (cleaned, no block yet).
	latest := readRc(t, LatestBackupPath(path))

	if strings.Contains(latest, "ai-shell") || strings.Contains(latest, Begin) ||
		!strings.Contains(latest, "export EDITOR=vim") {
		t.Fatalf("latest backup wrong:\n%s", latest)
	}
}

func TestShellInitSkipsUnsafeNames(t *testing.T) {
	out := ShellInit([]string{"claude", "bad;name", "$(evil)"})

	if strings.Contains(out, "bad;name") || strings.Contains(out, "$(evil)") {
		t.Fatalf("unsafe name emitted:\n%s", out)
	}

	if !strings.Contains(out, "claude()") {
		t.Fatalf("missing claude wrapper:\n%s", out)
	}
}

func TestBlockingInit(t *testing.T) {
	out := BlockingInit([]string{"claude"}, os.ErrNotExist)

	if !strings.Contains(out, "blocked") || !strings.Contains(out, `return 1`) {
		t.Fatalf("not a blocking init:\n%s", out)
	}

	if !strings.Contains(out, "claude()") {
		t.Fatalf("missing claude wrapper:\n%s", out)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"claude", "my-agent", "aider_v2", "x.y"} {
		if !ValidName(ok) {
			t.Fatalf("ValidName(%q) = false", ok)
		}
	}

	for _, bad := range []string{"", "a;b", "$(x)", "a b", "-x"} {
		if ValidName(bad) {
			t.Fatalf("ValidName(%q) = true", bad)
		}
	}
}

func TestShellInit(t *testing.T) {
	out := ShellInit([]string{"claude", "codex"})

	if !strings.Contains(out, "claude() { _oym_ai_jail claude \"$@\" }") {
		t.Fatalf("missing claude wrapper:\n%s", out)
	}

	if !strings.Contains(out, "codex() { _oym_ai_jail codex \"$@\" }") {
		t.Fatalf("missing codex wrapper:\n%s", out)
	}

	if strings.Contains(out, "vibe()") {
		t.Fatalf("unexpected vibe wrapper:\n%s", out)
	}

	if !strings.Contains(out, `command oym-ai-jail run "$@"`) {
		t.Fatalf("dispatcher missing:\n%s", out)
	}
}
