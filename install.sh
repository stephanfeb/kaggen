#!/bin/bash
set -euo pipefail

REPO="yourusername/kaggen"
BINARY="kaggen"
INSTALL_DIR="/usr/local/bin"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}==>${NC} $*"; }
warn()  { echo -e "${YELLOW}==>${NC} $*"; }
error() { echo -e "${RED}Error:${NC} $*" >&2; exit 1; }

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    arm64|aarch64) SUFFIX="darwin-arm64" ;;
    x86_64)        SUFFIX="darwin-amd64" ;;
    *)             error "Unsupported architecture: $ARCH" ;;
esac

OS=$(uname -s)
[ "$OS" != "Darwin" ] && error "This installer only supports macOS. Detected: $OS"

# Determine version
if [ -n "${VERSION:-}" ]; then
    TAG="$VERSION"
else
    info "Fetching latest release..."
    TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    [ -z "$TAG" ] && error "Could not determine latest version"
fi

info "Installing ${BINARY} ${TAG} (${SUFFIX})..."

# Download
TARBALL="${BINARY}-${SUFFIX}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${TARBALL}"
CHECKSUM_URL="https://github.com/${REPO}/releases/download/${TAG}/checksums.txt"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

info "Downloading ${URL}..."
curl -fsSL -o "${TMPDIR}/${TARBALL}" "$URL" || error "Download failed. Check that release ${TAG} exists."
curl -fsSL -o "${TMPDIR}/checksums.txt" "$CHECKSUM_URL" || error "Checksum download failed."

# Verify checksum
info "Verifying checksum..."
cd "$TMPDIR"
EXPECTED=$(grep "$TARBALL" checksums.txt | awk '{print $1}')
ACTUAL=$(shasum -a 256 "$TARBALL" | awk '{print $1}')
[ "$EXPECTED" != "$ACTUAL" ] && error "Checksum mismatch!\n  Expected: ${EXPECTED}\n  Got:      ${ACTUAL}"

# Extract
tar xzf "$TARBALL"

# Install
if [ -w "$INSTALL_DIR" ]; then
    mv "${BINARY}-${SUFFIX}" "${INSTALL_DIR}/${BINARY}"
else
    warn "Need sudo to install to ${INSTALL_DIR}"
    if command -v sudo &>/dev/null; then
        sudo mv "${BINARY}-${SUFFIX}" "${INSTALL_DIR}/${BINARY}"
        sudo chmod +x "${INSTALL_DIR}/${BINARY}"
    else
        INSTALL_DIR="${HOME}/.local/bin"
        mkdir -p "$INSTALL_DIR"
        mv "${BINARY}-${SUFFIX}" "${INSTALL_DIR}/${BINARY}"
        chmod +x "${INSTALL_DIR}/${BINARY}"
        warn "Installed to ${INSTALL_DIR}/${BINARY}"
        warn "Make sure ${INSTALL_DIR} is in your PATH"
    fi
fi

info "Installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"
echo

# Run interactive init
info "Running initial setup..."
echo
"${INSTALL_DIR}/${BINARY}" init

echo
info "Installation complete! Run 'kaggen agent' to start."
