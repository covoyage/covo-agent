#!/usr/bin/env bash
set -euo pipefail

APP=covo-agent
REPO=covoyage/covo-agent
INSTALL_DIR="$HOME/.covo-agent/bin"

MUTED='\033[0;2m'
RED='\033[0;31m'
ORANGE='\033[38;5;214m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

usage() {
    cat <<EOF
${APP} Installer

Usage: install.sh [options]

Options:
    -h, --help              Display this help message
    -v, --version <version> Install a specific version (e.g. 0.1.0)
    -b, --binary <path>     Install from a local binary instead of downloading
        --no-modify-path    Don't modify shell config files (.zshrc, .bashrc, etc.)

Examples:
    curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | bash
    curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | bash -s -- --version 0.1.0
    ./install.sh --binary /path/to/covo-agent
EOF
}

requested_version=""
no_modify_path=false
binary_path=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        -v|--version)
            if [[ -n "${2:-}" ]]; then
                requested_version="${2#v}"
                shift 2
            else
                echo -e "${RED}Error: --version requires a version argument${NC}"
                exit 1
            fi
            ;;
        -b|--binary)
            if [[ -n "${2:-}" ]]; then
                binary_path="$2"
                shift 2
            else
                echo -e "${RED}Error: --binary requires a path argument${NC}"
                exit 1
            fi
            ;;
        --no-modify-path)
            no_modify_path=true
            shift
            ;;
        *)
            echo -e "${ORANGE}Warning: Unknown option '$1'${NC}" >&2
            shift
            ;;
    esac
done

mkdir -p "$INSTALL_DIR"

detect_platform() {
    raw_os=$(uname -s)
    case "$raw_os" in
        Darwin*) os="darwin" ;;
        Linux*) os="linux" ;;
        MINGW*|MSYS*|CYGWIN*) os="windows" ;;
        *)
            echo -e "${RED}Unsupported OS: $raw_os${NC}"
            exit 1
            ;;
    esac

    raw_arch=$(uname -m)
    case "$raw_arch" in
        aarch64|arm64) arch="arm64" ;;
        x86_64|amd64) arch="x86_64" ;;
        *)
            echo -e "${RED}Unsupported architecture: $raw_arch${NC}"
            exit 1
            ;;
    esac

    case "$os-$arch" in
        linux-x86_64|linux-arm64|darwin-x86_64|darwin-arm64|windows-x86_64) ;;
        *)
            echo -e "${RED}Unsupported OS/Arch: $os/$arch${NC}"
            exit 1
            ;;
    esac
}

install_binary() {
    local src="$1"
    local dest="$INSTALL_DIR/$APP"
    if [ "${os:-}" = "windows" ]; then
        dest="$dest.exe"
    fi
    install -m 0755 "$src" "$dest"
    echo -e "${GREEN}Installed $APP to $dest${NC}"
    modify_path
    print_done
}

fetch_latest_version() {
    curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
        | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p'
}

verify_checksum() {
    local file="$1"
    local name="$2"
    if command -v sha256sum >/dev/null 2>&1; then
        local tool="sha256sum"
    elif command -v shasum >/dev/null 2>&1; then
        local tool="shasum -a 256"
    else
        echo -e "${ORANGE}Warning: no sha256 tool found; skipping checksum verification${NC}"
        return 0
    fi
    local expected
    expected=$(curl -fsSL "https://github.com/$REPO/releases/download/v$requested_version/checksums.txt" 2>/dev/null | grep -w "$name" | awk '{print $1}' || true)
    if [ -z "$expected" ]; then
        echo -e "${ORANGE}Warning: checksums.txt not found for v$requested_version; skipping verification${NC}"
        return 0
    fi
    local actual
    actual=$($tool "$file" | awk '{print $1}')
    if [ "$actual" != "$expected" ]; then
        echo -e "${RED}Error: checksum mismatch for $name${NC}" >&2
        rm -f "$file"
        exit 1
    fi
    echo -e "${MUTED}Checksum verified ($actual)${NC}"
}

download_and_install() {
    detect_platform

    if [ -z "$requested_version" ]; then
        echo -e "${MUTED}Fetching latest release…${NC}"
        requested_version=$(fetch_latest_version)
        if [ -z "$requested_version" ]; then
            echo -e "${RED}Error: failed to fetch the latest version from GitHub${NC}"
            exit 1
        fi
    fi

    local ext=".tar.gz"
    [ "$os" = "windows" ] && ext=".zip"

    local filename="${APP}_${requested_version}_${os}_${arch}${ext}"
    local url="https://github.com/$REPO/releases/download/v$requested_version/$filename"

    echo -e "\n${MUTED}Installing ${NC}${APP} ${MUTED}version: ${NC}$requested_version"

    local tmp_dir
    tmp_dir="${TMPDIR:-/tmp}/${APP}_install_$$"
    mkdir -p "$tmp_dir"
    trap 'rm -rf "$tmp_dir"' EXIT

    curl -fsSL -o "$tmp_dir/$filename" "$url"
    verify_checksum "$tmp_dir/$filename" "$filename"

    if [ "$os" = "windows" ]; then
        command -v unzip >/dev/null 2>&1 || { echo -e "${RED}Error: 'unzip' is required${NC}"; exit 1; }
        unzip -q "$tmp_dir/$filename" -d "$tmp_dir"
    else
        command -v tar >/dev/null 2>&1 || { echo -e "${RED}Error: 'tar' is required${NC}"; exit 1; }
        tar -xzf "$tmp_dir/$filename" -C "$tmp_dir"
    fi

    local binary="$tmp_dir/$APP"
    [ "$os" = "windows" ] && binary="$binary.exe"
    if [ ! -f "$binary" ]; then
        echo -e "${RED}Error: binary not found in archive${NC}"
        exit 1
    fi

    install_binary "$binary"
}

modify_path() {
    if [ "$no_modify_path" = "true" ]; then
        return
    fi
    case ":$PATH:" in
        *":$INSTALL_DIR:"*) return ;;
    esac

    local rc=""
    case "${SHELL:-}" in
        */zsh) rc="$HOME/.zshrc" ;;
        */bash) rc="$HOME/.bashrc" ;;
    esac
    [ -z "$rc" ] && rc="$HOME/.profile"

    {
        printf '\n# Added by the %s installer\nexport PATH="%s:$PATH"\n' "$APP" "$INSTALL_DIR"
    } >>"$rc"
    echo -e "${MUTED}Added $INSTALL_DIR to PATH in $rc (restart your shell to apply)${NC}"
}

print_done() {
    if [ "$no_modify_path" = "true" ] || [ -n "$binary_path" ]; then
        echo -e "\n${GREEN}Done! Run '$APP' from $INSTALL_DIR.${NC}"
    else
        echo -e "\n${GREEN}Done! Restart your shell, then run '${APP}'.${NC}"
    fi
    echo -e "${MUTED}To uninstall, remove $INSTALL_DIR and the PATH line from your shell config.${NC}"
}

# If --binary is provided, skip all download/detection logic.
if [ -n "$binary_path" ]; then
    if [ ! -f "$binary_path" ]; then
        echo -e "${RED}Error: Binary not found at ${binary_path}${NC}"
        exit 1
    fi
    install_binary "$binary_path"
    exit 0
fi

download_and_install
