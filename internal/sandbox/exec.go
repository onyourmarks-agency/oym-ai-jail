package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// SandboxExec is the macOS Seatbelt launcher shipped with the OS.
const SandboxExec = "/usr/bin/sandbox-exec"

// ActiveEnv marks processes already running inside the jail so wrappers and
// nested agents never try to apply a second Seatbelt profile (which macOS
// rejects with "Operation not permitted").
const ActiveEnv = "AI_JAIL_ACTIVE"

// Run replaces the current process with argv running under profile. When the
// process is already jailed it execs argv directly, mirroring the zsh wrapper.
func Run(profile string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("nothing to run")
	}

	if insideSandbox() {
		bin, err := exec.LookPath(argv[0])
		if err != nil {
			return err
		}
		return syscall.Exec(bin, argv, os.Environ())
	}

	env := withEnv(os.Environ(), ActiveEnv, "1")
	env = withEnv(env, "GIT_OPTIONAL_LOCKS", "0")
	argv = append([]string{SandboxExec, "-p", profile}, argv...)

	return syscall.Exec(SandboxExec, argv, env)
}

// insideSandbox verifies the process is really under Seatbelt instead of
// trusting AI_JAIL_ACTIVE — that variable can be forged, but a nested
// sandbox_apply cannot: macOS rejects it with EPERM when the outer profile
// restricts anything. The probe must be "(allow default)": a bare
// "(version 1)" profile denies everything and fails with EPERM even outside
// a jail, which would make the check useless. A process that is not jailed
// at all can always apply a permissive profile, so only an exit error counts
// as "sandboxed".
func insideSandbox() bool {
	err := exec.Command(SandboxExec, "-p", "(version 1)(allow default)", "/usr/bin/true").Run()
	var exitErr *exec.ExitError

	return errors.As(err, &exitErr)
}

// withEnv returns env with key=value set, replacing any existing entry.
func withEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := env[:0]

	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}

	return append(out, prefix+value)
}
