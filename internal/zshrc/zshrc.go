// Package zshrc maintains the managed oym-ai-jail block in ~/.zshrc and can
// clean up the older hand-installed ai-shell wrapper functions.
package zshrc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	Begin = "# >>> oym-ai-jail >>>"
	End   = "# <<< oym-ai-jail <<<"
)

// Block is the managed snippet. shell-init reads the YAML config on every
// new shell, so toggling agents never requires editing ~/.zshrc again.
const Block = Begin + `
command -v oym-ai-jail >/dev/null 2>&1 && eval "$(oym-ai-jail shell-init)"
` + End

// Path returns ~/.zshrc.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zshrc"), nil
}

func read(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// HasBlock reports whether the managed block is already present.
func HasBlock(path string) (bool, error) {
	content, err := read(path)
	if err != nil {
		return false, err
	}
	return strings.Contains(content, Begin), nil
}

// Ensure installs or refreshes the managed block. It returns whether the
// file was changed.
func Ensure(path string) (bool, error) {
	content, err := read(path)

	if err != nil {
		return false, err
	}

	if strings.Contains(content, Begin) {
		updated, found := replaceBetween(content, Begin, End, Block)
		if found {
			if updated == content {
				return false, nil
			}
			return true, write(path, updated)
		}
		// Begin without End: the block is corrupt. Strip the stray marker
		// lines and any leftover eval line — appending a fresh block next to
		// a surviving eval would run shell-init twice.
		lines := strings.Split(content, "\n")
		kept := lines[:0]
		for _, l := range lines {
			if t := strings.TrimSpace(l); t == Begin || t == End ||
				strings.Contains(l, "oym-ai-jail shell-init") {
				continue
			}
			kept = append(kept, l)
		}
		return true, write(path, appendBlock(strings.Join(kept, "\n")))
	}

	return true, write(path, appendBlock(content))
}

func appendBlock(content string) string {
	var b strings.Builder
	b.WriteString(content)

	if content != "" && !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}

	if content != "" {
		b.WriteString("\n")
	}

	b.WriteString(Block + "\n")
	return b.String()
}

// Remove deletes the managed block.
func Remove(path string) (bool, error) {
	content, err := read(path)

	if err != nil {
		return false, err
	}

	updated, found := replaceBetween(content, Begin, End, "")

	if !found {
		return false, nil
	}

	return true, write(path, collapseBlanks(updated))
}

func replaceBetween(content, begin, end, replacement string) (string, bool) {
	start := strings.Index(content, begin)

	if start < 0 {
		return content, false
	}

	rest := content[start+len(begin):]
	stop := strings.Index(rest, end)

	if stop < 0 {
		return content, false
	}

	stop += start + len(begin) + len(end)

	return content[:start] + replacement + content[stop:], true
}

var (
	// Legacy wrappers from the manual ai-shell setup, e.g. "claude() {".
	funcRe    = regexp.MustCompile(`^(?:function[ \t]+)?([a-zA-Z0-9_-]+)[ \t]*(\(\))?[ \t]*\{`)
	legacyHit = "ai-shell"
)

// HasLegacy reports whether the file still contains a wrapper function that
// delegates to the old hand-installed ai-shell script.
func HasLegacy(path string) (bool, error) {
	content, err := read(path)
	if err != nil {
		return false, err
	}

	return strings.Contains(content, legacyHit), nil
}

// RemoveLegacy strips wrapper functions that call the old ai-shell script,
// together with their surrounding banner comments. It returns whether the
// file changed.
func RemoveLegacy(path string) (bool, error) {
	content, err := read(path)

	if err != nil {
		return false, err
	}

	if !strings.Contains(content, legacyHit) {
		return false, nil
	}

	lines := strings.Split(content, "\n")
	drop := make([]bool, len(lines))

	for i, line := range lines {
		if !funcRe.MatchString(line) {
			continue
		}
		// Collect the function body by tracking brace depth, so one-line
		// definitions like `claude() { ai-shell claude "$@" }` end on their
		// own line and nested braces don't cut the body short. An
		// unterminated function is left untouched rather than risking the
		// rest of the file.
		depth, end := 0, -1
		for j := i; j < len(lines); j++ {
			depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
			if depth <= 0 {
				end = j
				break
			}
		}

		if end < 0 {
			continue
		}

		body := strings.Join(lines[i:end+1], "\n")

		if !strings.Contains(body, legacyHit) {
			continue
		}

		for j := i; j <= end && j < len(lines); j++ {
			drop[j] = true
		}

		// Swallow the comment banner directly above the function.
		for j := i - 1; j >= 0; j-- {
			l := strings.TrimSpace(lines[j])
			if strings.HasPrefix(l, "#") && (strings.Contains(l, "====") ||
				strings.Contains(strings.ToUpper(l), "JAIL")) {
				drop[j] = true
				continue
			}
			break
		}
	}

	var kept []string
	for i, line := range lines {
		if !drop[i] {
			kept = append(kept, line)
		}
	}

	updated := collapseBlanks(strings.Join(kept, "\n"))

	if updated == content {
		return false, nil
	}

	return true, write(path, updated)
}

func collapseBlanks(content string) string {
	return regexp.MustCompile(`\n{3,}`).ReplaceAllString(content, "\n\n")
}

// BackupPath is where the original pre-migration .zshrc goes (written once).
func BackupPath(path string) string {
	return path + ".oym-backup"
}

// LatestBackupPath holds the .zshrc from just before the most recent write.
func LatestBackupPath(path string) string {
	return path + ".oym-backup-latest"
}

func write(path, content string) error {
	// Keep two backups: the original file once (a second write in the same
	// migration must not overwrite it) and a rolling copy for later undos.
	if existing, err := os.ReadFile(path); err == nil {
		if _, err := os.Stat(BackupPath(path)); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(BackupPath(path), existing, 0o600); err != nil {
				return fmt.Errorf("backing up %s: %w", path, err)
			}
		}
		if err := os.WriteFile(LatestBackupPath(path), existing, 0o600); err != nil {
			return fmt.Errorf("backing up %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// validName matches names that are safe to emit as a zsh function name —
// a hostile or mistyped config entry must never inject code into the eval.
var validNameRe = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`)

// ValidName reports whether name can safely become a zsh function name.
func ValidName(name string) bool {
	return validNameRe.MatchString(name)
}

// ShellInit renders the zsh functions for the enabled agent names. The eval'd
// output is regenerated per shell from the YAML config.
func ShellInit(names []string) string {
	var b strings.Builder
	b.WriteString("# Generated by oym-ai-jail — changes come from ~/.config/oym-ai-jail/config.yaml\n")
	// Always go through `oym-ai-jail run`: it resolves agent-specific Args
	// (e.g. Codex's --sandbox flag) and handles AI_JAIL_ACTIVE itself.
	b.WriteString(`_oym_ai_jail() {
  command oym-ai-jail run "$@"
}` + "\n")
	for _, name := range names {
		if !ValidName(name) {
			continue
		}
		fmt.Fprintf(&b, "%s() { _oym_ai_jail %s \"$@\" }\n", name, name)
	}
	return b.String()
}

// BlockingInit renders wrappers that refuse to run the named agents: used
// when the config cannot be read, so a broken setup fails closed instead of
// leaving the agents unjailed.
func BlockingInit(names []string, cause error) string {
	var b strings.Builder
	b.WriteString("# oym-ai-jail could not load its config — agents are blocked.\n")
	b.WriteString(`_oym_ai_jail() {
  echo "oym-ai-jail: ` + quote(cause.Error()) + `" >&2
  return 1
}` + "\n")
	for _, name := range names {
		if !ValidName(name) {
			continue
		}
		fmt.Fprintf(&b, "%s() { _oym_ai_jail %s \"$@\" }\n", name, name)
	}
	return b.String()
}

// quote makes a string safe inside double quotes in generated zsh.
func quote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", "\\$", "!", "\\!").Replace(s)
}
