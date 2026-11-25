#!/usr/bin/env bash
# Installs the latest oym-ai-jail release binary and runs the setup TUI.
#
#   curl -fsSL https://raw.githubusercontent.com/onyourmarks-agency/oym-ai-jail/main/install.sh | bash
#
# Options via environment:
#   INSTALL_DIR          target directory (default: /usr/local/bin, falls back to ~/.local/bin)
#   GITHUB_TOKEN         used for API/download auth when the gh CLI is not installed
#   OYM_AI_JAIL_NO_SETUP skip the interactive setup after installing
set -euo pipefail

REPO="onyourmarks-agency/oym-ai-jail"
BINARY="oym-ai-jail"

case "$(uname -s)" in
    Darwin) OS="darwin" ;;
    *) echo "oym-ai-jail is macOS-only ($(uname -s) unsupported)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
    arm64|aarch64) ARCH="arm64" ;;
    x86_64|amd64) ARCH="amd64" ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

auth_curl() {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN}" "$@"
    else
        curl -fsSL "$@"
    fi
}

# json_field extracts a top-level string field from a JSON document on stdin.
json_field() {
    if command -v jq >/dev/null 2>&1; then
        jq -r ".${1} // empty"
    else
        grep -m1 "\"${1}\"" | sed -E 's/.*"'"${1}"'": *"([^"]+)".*/\1/'
    fi
}

# asset_url extracts the API URL of one release asset from a JSON release on
# stdin. The fallback splits on object boundaries so it works on compact JSON.
asset_url() {
    if command -v jq >/dev/null 2>&1; then
        jq -r --arg a "${1}" '.assets[] | select(.name == $a) | .url'
    else
        tr '}' '\n' | grep "\"name\": *\"${1}\"" | sed -n 's/.*"url": *"\([^"]*\)".*/\1/p' | head -1
    fi
}

# gh refuses to run unauthenticated even against public repos, so only use it
# when it can actually authenticate.
USE_GH=false
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    USE_GH=true
fi

if [ "${USE_GH}" = true ]; then
    TAG="$(gh api "repos/${REPO}/releases/latest" --jq .tag_name)"
else
    TAG="$(auth_curl "https://api.github.com/repos/${REPO}/releases/latest" | json_field tag_name)"
fi
if [ -z "${TAG}" ]; then
    echo "could not resolve the latest release tag" >&2
    exit 1
fi

ASSET="${BINARY}-${TAG}-${OS}-${ARCH}.tar.gz"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}"' EXIT

# download <asset-name> -> ${WORKDIR}/<asset-name>
download() {
    local name="$1"
    if [ "${USE_GH}" = true ]; then
        gh release download "${TAG}" --repo "${REPO}" --pattern "${name}" --output "${WORKDIR}/${name}" --clobber
    elif [ -n "${GITHUB_TOKEN:-}" ]; then
        # Private repos: the browser download URL ignores tokens; use the asset API.
        local url
        url="$(auth_curl "https://api.github.com/repos/${REPO}/releases/tags/${TAG}" | asset_url "${name}")"
        if [ -z "${url}" ]; then
            echo "asset ${name} not found in release ${TAG}" >&2
            return 1
        fi
        curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN}" -H "Accept: application/octet-stream" -o "${WORKDIR}/${name}" "${url}"
    else
        auth_curl -o "${WORKDIR}/${name}" "https://github.com/${REPO}/releases/download/${TAG}/${name}"
    fi
}

echo "downloading ${ASSET} (${TAG})"
download "${ASSET}"
if download "CHECKSUM" 2>/dev/null; then
    EXPECTED="$(grep " ${ASSET}\$" "${WORKDIR}/CHECKSUM" | awk '{print $1}' || true)"
    if [ -z "${EXPECTED}" ]; then
        echo "CHECKSUM has no entry for ${ASSET}" >&2
        exit 1
    fi
    ACTUAL="$(shasum -a 256 "${WORKDIR}/${ASSET}" | awk '{print $1}')"
    if [ "${ACTUAL}" != "${EXPECTED}" ]; then
        echo "checksum mismatch for ${ASSET}" >&2
        exit 1
    fi
else
    echo "warning: release has no CHECKSUM asset; skipping verification" >&2
fi
tar -xzf "${WORKDIR}/${ASSET}" -C "${WORKDIR}"

if [ -n "${INSTALL_DIR:-}" ]; then
    DEST="${INSTALL_DIR}"
    if [ ! -d "${DEST}" ]; then
        mkdir -p "${DEST}"
    fi
    if [ ! -w "${DEST}" ]; then
        echo "INSTALL_DIR ${DEST} is not writable" >&2
        exit 1
    fi
elif [ -w /usr/local/bin ]; then
    DEST="/usr/local/bin"
else
    DEST="${HOME}/.local/bin"
    mkdir -p "${DEST}"
fi
install -m 0755 "${WORKDIR}/${BINARY}" "${DEST}/${BINARY}"

echo "installed $("${DEST}/${BINARY}" version | head -1) to ${DEST}/${BINARY}"
case ":${PATH}:" in
    *":${DEST}:"*) ;;
    *) echo "note: add ${DEST} to your PATH" ;;
esac

# stdin is the pipe from curl, so attach the TUI to the real terminal instead.
if [ -z "${OYM_AI_JAIL_NO_SETUP:-}" ] && [ -e /dev/tty ]; then
    echo ""
    "${DEST}/${BINARY}" < /dev/tty
fi
