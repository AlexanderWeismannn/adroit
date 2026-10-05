#!/usr/bin/env bash
#
# Install Adroit.
#
#   curl -fsSL https://raw.githubusercontent.com/AlexanderWeismannn/adroit/main/install.sh | bash
#
# Options (pass them after `bash -s --` when piping):
#   --version X.Y.Z   install that release instead of the latest
#   --bin-dir DIR     install into DIR (default: ~/.local/bin, or $BIN_DIR)
#   --with-cs         also link `cs`, for muscle memory carried over from claude-squad
#   --install-deps    install missing tmux / gh with the system package manager
#   --no-path         do not add the bin dir to your shell profile
#
# The binary comes from GitHub Releases and is checked against checksums.txt.
# With no release for this platform it falls back to `go install` when Go is
# present.

set -euo pipefail

REPO="AlexanderWeismannn/adroit"
MODULE="github.com/${REPO}"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
VERSION=""
WITH_CS=0
INSTALL_DEPS=0
EDIT_PATH=1
PATH_NOTE=""
TMP_DIR=""

say() { printf '%s\n' "$*"; }
step() { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Install Adroit.

  curl -fsSL https://raw.githubusercontent.com/AlexanderWeismannn/adroit/main/install.sh | bash -s -- [options]

  --version X.Y.Z   install that release instead of the latest
  --bin-dir DIR     install into DIR (default: ~/.local/bin, or $BIN_DIR)
  --with-cs         also link `cs`, for muscle memory carried over from claude-squad
  --install-deps    install missing tmux / gh with the system package manager
  --no-path         do not add the bin dir to your shell profile
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --version) VERSION="${2#v}"; shift 2 ;;
        --bin-dir) BIN_DIR="$2"; shift 2 ;;
        --with-cs) WITH_CS=1; shift ;;
        --install-deps) INSTALL_DEPS=1; shift ;;
        --no-path) EDIT_PATH=0; shift ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown option: $1 (try --help)" ;;
    esac
done

cleanup() { if [ -n "$TMP_DIR" ]; then rm -rf "$TMP_DIR"; fi; }
trap cleanup EXIT

need() { command -v "$1" >/dev/null 2>&1; }

detect_platform() {
    case "$(uname -s)" in
        Linux) OS=linux ;;
        Darwin) OS=darwin ;;
        MINGW*|MSYS*|CYGWIN*)
            die "Adroit needs tmux, so native Windows is not supported. Install it inside WSL: https://learn.microsoft.com/windows/wsl/install" ;;
        *) die "unsupported OS: $(uname -s)" ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64)
            ARCH=amd64
            # An x86_64 shell under Rosetta on Apple silicon: take the native build.
            if [ "$OS" = darwin ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
                ARCH=arm64
            fi ;;
        arm64|aarch64) ARCH=arm64 ;;
        *) die "unsupported CPU architecture: $(uname -m)" ;;
    esac
}

fetch() {
    if need curl; then curl -fsSL "$1"
    elif need wget; then wget -qO- "$1"
    else die "need curl or wget"
    fi
}

download() {
    if need curl; then curl -fsSL -o "$2" "$1"
    else wget -qO "$2" "$1"
    fi
}

sha256() {
    if need sha256sum; then sha256sum "$1" | cut -d' ' -f1
    else shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

latest_version() {
    fetch "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
        | grep -m1 '"tag_name":' | sed -E 's/.*"v?([^"]+)".*/\1/'
}

install_from_release() {
    if [ -z "$VERSION" ]; then
        VERSION="$(latest_version || true)"
        if [ -z "$VERSION" ]; then return 1; fi
    fi
    local archive="adroit_${VERSION}_${OS}_${ARCH}.tar.gz"
    local base="https://github.com/${REPO}/releases/download/v${VERSION}"
    TMP_DIR="$(mktemp -d)"

    step "Downloading Adroit ${VERSION} (${OS}/${ARCH})"
    download "${base}/${archive}" "${TMP_DIR}/${archive}" || return 1
    download "${base}/checksums.txt" "${TMP_DIR}/checksums.txt" || die "could not download checksums.txt"

    local want got
    want="$(grep " ${archive}\$" "${TMP_DIR}/checksums.txt" | cut -d' ' -f1 || true)"
    got="$(sha256 "${TMP_DIR}/${archive}")"
    if [ -z "$want" ] || [ "$want" != "$got" ]; then
        die "checksum mismatch for ${archive}"
    fi

    tar -xzf "${TMP_DIR}/${archive}" -C "$TMP_DIR" adroit
    mkdir -p "$BIN_DIR"
    install -m 0755 "${TMP_DIR}/adroit" "${BIN_DIR}/adroit"
}

install_with_go() {
    need go || return 1
    local ref="latest"
    if [ -n "$VERSION" ]; then ref="v${VERSION}"; fi
    step "Building with go install (${MODULE}@${ref})"
    mkdir -p "$BIN_DIR"
    GOBIN="$BIN_DIR" go install "${MODULE}@${ref}"
}

pkg_install() {
    if [ "$OS" = darwin ]; then
        if ! need brew; then warn "Homebrew not found (https://brew.sh); install $1 yourself"; return 1; fi
        brew install "$1"
    elif need apt-get; then sudo apt-get update -qq && sudo apt-get install -y "$1"
    elif need dnf; then sudo dnf install -y "$1"
    elif need pacman; then sudo pacman -S --noconfirm "$1"
    elif need zypper; then sudo zypper install -y "$1"
    elif need apk; then sudo apk add "$1"
    else warn "no known package manager; install $1 yourself"; return 1
    fi
}

install_deps() {
    if ! need tmux; then
        step "Installing tmux"
        pkg_install tmux || true
    fi
    if ! need gh; then
        if [ "$OS" = linux ] && need apt-get; then
            # Debian/Ubuntu's packaged gh is years old; GitHub's own apt repo is the supported route.
            warn "gh is not installed: see https://github.com/cli/cli/blob/trunk/docs/install_linux.md"
        else
            step "Installing gh"
            pkg_install gh || true
        fi
    fi
}

ensure_path() {
    case ":$PATH:" in *":$BIN_DIR:"*) return 0 ;; esac
    local profile line
    case "${SHELL:-}" in
        */zsh) profile="$HOME/.zshrc"; line="export PATH=\"$BIN_DIR:\$PATH\"" ;;
        */bash) profile="$HOME/.bashrc"; line="export PATH=\"$BIN_DIR:\$PATH\"" ;;
        */fish) profile="$HOME/.config/fish/config.fish"; line="fish_add_path $BIN_DIR" ;;
        *) profile="$HOME/.profile"; line="export PATH=\"$BIN_DIR:\$PATH\"" ;;
    esac
    if [ "$EDIT_PATH" = 1 ]; then
        mkdir -p "$(dirname "$profile")"
        if ! grep -qsF "$line" "$profile"; then
            printf '\n# Added by the Adroit installer\n%s\n' "$line" >> "$profile"
            say "Added $BIN_DIR to PATH in $profile"
        fi
        PATH_NOTE="Open a new terminal (or run: source $profile) so \`adroit\` is on your PATH."
    else
        PATH_NOTE="$BIN_DIR is not on your PATH. Add it: $line"
    fi
}

main() {
    detect_platform

    if ! install_from_release; then
        if [ -n "$VERSION" ]; then
            warn "could not download release v${VERSION} for ${OS}/${ARCH}"
        else
            warn "no published release found for ${OS}/${ARCH}"
        fi
        install_with_go || die "Go is not installed, so it cannot be built either. Install Go 1.23+ (https://go.dev/dl/) and re-run, or build from source (see the README)."
    fi

    if [ "$WITH_CS" = 1 ]; then
        ln -sf "${BIN_DIR}/adroit" "${BIN_DIR}/cs"
        say "Linked ${BIN_DIR}/cs -> adroit"
    fi

    if [ "$INSTALL_DEPS" = 1 ]; then install_deps; fi
    ensure_path

    say ""
    step "Installed $("${BIN_DIR}/adroit" version | head -1) to ${BIN_DIR}/adroit"
    say ""
    # doctor exits non-zero when a required tool is missing; the install itself still succeeded.
    "${BIN_DIR}/adroit" doctor 2>/dev/null || true
    if [ -n "$PATH_NOTE" ]; then say ""; say "$PATH_NOTE"; fi
    say ""
    say "Next: cd into a git repository and run \`adroit\`. Press ? inside for help."
}

main
