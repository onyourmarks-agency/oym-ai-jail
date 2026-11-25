package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsNotFound(t *testing.T) {
	cfg, found, err := LoadFile(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil || found || cfg != nil {
		t.Fatalf("expected (nil, false, nil), got (%v, %v, %v)", cfg, found, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	in := &Config{
		Version:             1,
		Workspaces:          []string{"~/dev/one", "~/dev/two"},
		AllowWorkspaceRead:  true,
		AllowWorkspaceWrite: true,
		ReadPaths:           []string{"~/ai-allowed"},
		WritePaths:          []string{"/shared"},
		Agents:              map[string]bool{"claude": true, "vibe": false},
		CustomAgents: []CustomAgent{
			{Name: "aider", RWPaths: []string{"~/.aider"}},
		},
	}
	if err := in.SaveFile(path); err != nil {
		t.Fatal(err)
	}

	out, found, err := LoadFile(path)

	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}

	if !out.AllowWorkspaceWrite || len(out.Workspaces) != 2 || out.CustomAgents[0].Name != "aider" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}

	if out.Agents["claude"] != true || out.Agents["vibe"] != false {
		t.Fatalf("agents mismatch: %+v", out.Agents)
	}
}

func TestExpandHome(t *testing.T) {
	if got := ExpandHome("~", "/h"); got != "/h" {
		t.Fatal(got)
	}

	if got := ExpandHome("~/x/y", "/h"); got != "/h/x/y" {
		t.Fatal(got)
	}

	if got := ExpandHome("/abs", "/h"); got != "/abs" {
		t.Fatal(got)
	}
}

func TestCanonicalResolvesSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(base, "link")

	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	want, err := filepath.EvalSymlinks(real)

	if err != nil {
		t.Fatal(err)
	}
	if got := Canonical(filepath.Join(link, "child"), "/nonexistent-home"); got != filepath.Join(want, "child") {
		t.Fatalf("got %s", got)
	}

	// macOS: /tmp is a symlink to /private/tmp.
	if got := Canonical("/tmp/oym-x", "/nonexistent-home"); got != "/private/tmp/oym-x" {
		t.Fatalf("got %s", got)
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a", false},
		{"/a/b/", "/a/b/c/d", true},
	}

	for _, c := range cases {
		if got := Within(c.root, c.path); got != c.want {
			t.Fatalf("Within(%q,%q)=%v want %v", c.root, c.path, got, c.want)
		}
	}
}

func TestProject(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())

	if err != nil {
		t.Fatal(err)
	}

	ws := filepath.Join(home, "dev")
	project := filepath.Join(ws, "klant-a")

	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Resolved{Home: home, Workspaces: []string{ws}}

	p, matched, in, atRoot := r.Project(filepath.Join(project, "sub", "dir"))

	if !in || atRoot || p != filepath.Join(project, "sub", "dir") || matched != ws {
		t.Fatalf("got (%s, %s, %v, %v)", p, matched, in, atRoot)
	}

	p, matched, in, atRoot = r.Project(ws)

	if !in || !atRoot || p != ws || matched != ws {
		t.Fatalf("workspace root: got (%s, %s, %v, %v)", p, matched, in, atRoot)
	}

	p, matched, in, _ = r.Project(filepath.Join(home, "elsewhere"))

	if in || p != "/private/tmp" || matched != "" {
		t.Fatalf("outside workspace: got (%s, %s, %v)", p, matched, in)
	}
}
