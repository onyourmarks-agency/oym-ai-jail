// Package selfupdate replaces the running binary with the latest GitHub
// release asset.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	RepoSlug   = "onyourmarks-agency/oym-ai-jail"
	binaryName = "oym-ai-jail"

	apiBase        = "https://api.github.com/repos/" + RepoSlug
	downloadFormat = "https://github.com/" + RepoSlug + "/releases/download/%s/%s"
)

// LatestTag resolves the latest release tag, preferring the gh CLI (which
// handles private-repo auth) and falling back to the GitHub API with an
// optional GITHUB_TOKEN.
func LatestTag(ctx context.Context) (string, error) {
	if ghPath, err := exec.LookPath("gh"); err == nil {
		out, ghErr := exec.CommandContext(ctx, ghPath, "api", "repos/"+RepoSlug+"/releases/latest", "--jq", ".tag_name").Output()
		if ghErr == nil {
			if tag := strings.TrimSpace(string(out)); tag != "" {
				return tag, nil
			}
		}
	}

	body, err := apiGet(ctx, apiBase+"/releases/latest", "")

	if err != nil {
		return "", err
	}

	defer body.Close()
	var release struct {
		TagName string `json:"tag_name"`
	}

	if err := json.NewDecoder(body).Decode(&release); err != nil {
		return "", err
	}

	if release.TagName == "" {
		return "", errors.New("release has no tag name")
	}

	return release.TagName, nil
}

// Run downloads the release archive for darwin/<arch> and replaces the
// running binary.
func Run(ctx context.Context, currentVersion string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("self-update is only supported on macOS (running %s)", runtime.GOOS)
	}

	tag, err := LatestTag(ctx)

	if err != nil {
		return fmt.Errorf("resolving latest release: %w", err)
	}

	if !versionAhead(tag, currentVersion) {
		fmt.Printf("already up to date (%s)\n", currentVersion)
		return nil
	}

	assetName := fmt.Sprintf("%s-%s-%s-%s.tar.gz", binaryName, tag, runtime.GOOS, runtime.GOARCH)
	archivePath, err := downloadAsset(ctx, tag, assetName)

	if err != nil {
		return err
	}

	defer os.Remove(archivePath)

	if err := verifyChecksum(ctx, tag, assetName, archivePath); err != nil {
		return err
	}

	executable, err := os.Executable()

	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}

	// Stage in the target's own directory so the final swap is a
	// same-filesystem rename: atomic, and valid over a running binary.
	staged, err := extractBinary(archivePath, filepath.Dir(executable))

	if err != nil {
		return err
	}

	if err := os.Rename(staged, executable); err != nil {
		os.Remove(staged)
		return fmt.Errorf("replacing %s: %w (retry with elevated permissions or reinstall via install.sh)", executable, err)
	}

	fmt.Printf("updated %s -> %s (%s)\n", currentVersion, tag, executable)

	return nil
}

func downloadAsset(ctx context.Context, tag string, assetName string) (string, error) {
	temp, err := os.CreateTemp("", assetName+".*")
	if err != nil {
		return "", err
	}

	defer temp.Close()

	if ghPath, lookErr := exec.LookPath("gh"); lookErr == nil {
		ghErr := exec.CommandContext(ctx, ghPath, "release", "download", tag,
			"--repo", RepoSlug, "--pattern", assetName, "--output", temp.Name(), "--clobber").Run()
		if ghErr == nil {
			return temp.Name(), nil
		}
	}

	// The browser download URL ignores Authorization and 404s on private
	// repos, so a token must go through the release-asset API instead.
	var body io.ReadCloser

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		body, err = openAssetViaAPI(ctx, tag, assetName, token)
	} else {
		body, err = apiGet(ctx, fmt.Sprintf(downloadFormat, tag, assetName), "")
	}
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", assetName, err)
	}
	defer body.Close()

	if _, err := io.Copy(temp, body); err != nil {
		return "", err
	}

	return temp.Name(), nil
}

func openAssetViaAPI(ctx context.Context, tag string, assetName string, token string) (io.ReadCloser, error) {
	releaseBody, err := apiGet(ctx, apiBase+"/releases/tags/"+tag, token)

	if err != nil {
		return nil, err
	}

	defer releaseBody.Close()
	var release struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"assets"`
	}

	if err := json.NewDecoder(releaseBody).Decode(&release); err != nil {
		return nil, err
	}

	for _, asset := range release.Assets {
		if asset.Name == assetName {
			return apiGet(ctx, asset.URL, token, "application/octet-stream")
		}
	}

	return nil, fmt.Errorf("release %s has no asset %s", tag, assetName)
}

func apiGet(ctx context.Context, url string, token string, accept ...string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

	if err != nil {
		return nil, err
	}

	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	if len(accept) > 0 {
		request.Header.Set("Accept", accept[0])
	}
	response, err := http.DefaultClient.Do(request)

	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, response.Status)
	}

	return response.Body, nil
}

func extractBinary(archivePath string, stagingDir string) (string, error) {
	archive, err := os.Open(archivePath)

	if err != nil {
		return "", err
	}

	defer archive.Close()

	gzipReader, err := gzip.NewReader(archive)

	if err != nil {
		return "", err
	}

	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	for {
		header, err := tarReader.Next()

		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("binary %q not found in release archive", binaryName)
		}

		if err != nil {
			return "", err
		}

		if filepath.Base(header.Name) != binaryName || header.Typeflag != tar.TypeReg {
			continue
		}

		temp, err := os.CreateTemp(stagingDir, "."+binaryName+".*")

		if err != nil {
			return "", err
		}

		if _, err := io.Copy(temp, tarReader); err != nil {
			temp.Close()
			os.Remove(temp.Name())
			return "", err
		}

		if err := temp.Chmod(0o755); err != nil {
			temp.Close()
			os.Remove(temp.Name())
			return "", err
		}

		if err := temp.Close(); err != nil {
			os.Remove(temp.Name())
			return "", err
		}

		return temp.Name(), nil
	}
}

// NewerAvailable reports a newer tag when one exists; every failure path
// returns empty so callers can stay silent. Disabled for dev builds and via
// OYM_AI_JAIL_NO_UPDATE_CHECK.
func NewerAvailable(currentVersion string) string {
	if currentVersion == "dev" || os.Getenv("OYM_AI_JAIL_NO_UPDATE_CHECK") != "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tag, err := LatestTag(ctx)

	if err != nil || !versionAhead(tag, currentVersion) {
		return ""
	}

	return tag
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func parseVersion(v string) ([3]int, bool) {
	m := semverRe.FindStringSubmatch(v)

	if m == nil {
		return [3]int{}, false
	}
	var out [3]int

	for i, part := range m[1:] {
		out[i], _ = strconv.Atoi(part)
	}

	return out, true
}

// versionAhead reports whether candidate is strictly newer than current.
// Unparseable candidates never count; an unparseable current (a dev or ad-hoc
// build) counts as older than any tag. Equal triples are never an update — a
// git-describe suffix like "-4-gdeadbee" on current even means commits past
// the tag, so updating would downgrade.
func versionAhead(candidate, current string) bool {
	c, ok := parseVersion(candidate)

	if !ok {
		return false
	}

	u, ok := parseVersion(current)

	if !ok {
		return true
	}
	for i := range c {
		if c[i] != u[i] {
			return c[i] > u[i]
		}
	}

	return false
}

// verifyChecksum downloads the release's CHECKSUM file (published by
// GoReleaser) and compares the archive's SHA-256 against its entry.
func verifyChecksum(ctx context.Context, tag, assetName, archivePath string) error {
	sumPath, err := downloadAsset(ctx, tag, "CHECKSUM")
	if err != nil {
		return fmt.Errorf("downloading release checksums: %w", err)
	}

	defer os.Remove(sumPath)

	data, err := os.ReadFile(sumPath)

	if err != nil {
		return err
	}

	want := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			want = fields[0]
			break
		}
	}

	if want == "" {
		return fmt.Errorf("release checksums have no entry for %s", assetName)
	}

	archive, err := os.Open(archivePath)

	if err != nil {
		return err
	}

	defer archive.Close()
	h := sha256.New()

	if _, err := io.Copy(h, archive); err != nil {
		return err
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, want)
	}

	return nil
}
