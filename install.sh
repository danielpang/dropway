#!/bin/sh
# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Dropway CLI installer. Detects your OS/arch, downloads the matching `dropway`
# binary from GitHub Releases, verifies its checksum, and installs it onto PATH.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/danielpang/dropway/main/install.sh | sh
#
# Environment overrides:
#   DROPWAY_VERSION      release tag to install (default: latest), e.g. v0.1.0
#   DROPWAY_INSTALL_DIR  install directory (default: /usr/local/bin, falling
#                        back to ~/.local/bin when that isn't writable)
#
# POSIX sh — no bashisms, so it runs under dash/sh on minimal systems.

set -eu

REPO="danielpang/dropway"
BIN="dropway"

# ---- pretty output --------------------------------------------------------
# Colors only when stderr is a TTY (piped installs stay clean).
if [ -t 2 ]; then
  BOLD="$(printf '\033[1m')"; RED="$(printf '\033[31m')"
  GREEN="$(printf '\033[32m')"; DIM="$(printf '\033[2m')"; RESET="$(printf '\033[0m')"
else
  BOLD=""; RED=""; GREEN=""; DIM=""; RESET=""
fi
info() { printf '%s\n' "${DIM}dropway:${RESET} $*" >&2; }
ok()   { printf '%s\n' "${GREEN}dropway:${RESET} $*" >&2; }
err()  { printf '%s\n' "${RED}${BOLD}dropway:${RESET} $*" >&2; }
die()  { err "$*"; exit 1; }

# ---- prerequisites --------------------------------------------------------
# Need one of curl/wget for downloads and a sha256 tool for verification.
if command -v curl >/dev/null 2>&1; then
  http_get() { curl -fsSL "$1"; }                  # to stdout
  http_dl()  { curl -fsSL -o "$2" "$1"; }          # to file
elif command -v wget >/dev/null 2>&1; then
  http_get() { wget -qO- "$1"; }
  http_dl()  { wget -qO "$2" "$1"; }
else
  die "need curl or wget installed to download the binary."
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  sha256() { echo ""; }   # no tool → checksum step is skipped with a warning
fi

# ---- detect platform ------------------------------------------------------
os="$(uname -s)"
case "$os" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *) die "unsupported OS '$os' — Dropway ships binaries for Linux and macOS. Build from source: go install github.com/${REPO}/cli/cmd/dropway@latest" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64)  arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) die "unsupported architecture '$arch' — only amd64 and arm64 are published." ;;
esac

asset="${BIN}_${os}_${arch}"

# ---- resolve version / URLs ----------------------------------------------
version="${DROPWAY_VERSION:-latest}"
if [ "$version" = "latest" ]; then
  base="https://github.com/${REPO}/releases/latest/download"
else
  base="https://github.com/${REPO}/releases/download/${version}"
fi
bin_url="${base}/${asset}"
sum_url="${base}/checksums.txt"

info "installing ${BOLD}${asset}${RESET} (${version}) from ${REPO}"

# ---- download into a temp dir we always clean up --------------------------
tmp="$(mktemp -d 2>/dev/null || mktemp -d -t dropway)"
trap 'rm -rf "$tmp"' EXIT INT TERM

info "downloading ${bin_url}"
http_dl "$bin_url" "${tmp}/${BIN}" \
  || die "download failed. Is ${version} published with a ${asset} asset? See https://github.com/${REPO}/releases"

# ---- verify checksum (best-effort: warn, don't fail, if unavailable) ------
got="$(sha256 "${tmp}/${BIN}")"
if [ -z "$got" ]; then
  err "no sha256 tool found — skipping checksum verification."
elif http_dl "$sum_url" "${tmp}/checksums.txt" 2>/dev/null; then
  want="$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
  if [ -z "$want" ]; then
    err "no checksum entry for ${asset} — skipping verification."
  elif [ "$got" != "$want" ]; then
    die "checksum mismatch for ${asset}!
  expected: ${want}
  actual:   ${got}
Refusing to install a binary that doesn't match the published checksum."
  else
    info "checksum verified"
  fi
else
  err "couldn't fetch checksums.txt — skipping verification."
fi

chmod +x "${tmp}/${BIN}"

# ---- pick an install dir and place the binary -----------------------------
# Honor an explicit override; otherwise prefer a writable /usr/local/bin, then
# fall back to ~/.local/bin (no sudo). If only /usr/local/bin works but isn't
# writable, use sudo when available.
install_to() {
  dir="$1"
  if [ -d "$dir" ] && [ -w "$dir" ]; then
    mv "${tmp}/${BIN}" "${dir}/${BIN}"; return 0
  fi
  return 1
}

dest=""
if [ -n "${DROPWAY_INSTALL_DIR:-}" ]; then
  mkdir -p "$DROPWAY_INSTALL_DIR" 2>/dev/null || true
  install_to "$DROPWAY_INSTALL_DIR" \
    || die "can't write to DROPWAY_INSTALL_DIR=${DROPWAY_INSTALL_DIR}"
  dest="$DROPWAY_INSTALL_DIR"
elif install_to "/usr/local/bin"; then
  dest="/usr/local/bin"
elif command -v sudo >/dev/null 2>&1 && [ -d /usr/local/bin ]; then
  info "elevating with sudo to write /usr/local/bin"
  sudo mv "${tmp}/${BIN}" "/usr/local/bin/${BIN}"
  dest="/usr/local/bin"
else
  dest="${HOME}/.local/bin"
  mkdir -p "$dest"
  mv "${tmp}/${BIN}" "${dest}/${BIN}"
fi

ok "installed to ${BOLD}${dest}/${BIN}${RESET}"

# ---- PATH hint + confirm it runs ------------------------------------------
case ":${PATH}:" in
  *":${dest}:"*) ;;  # already on PATH
  *) info "add ${dest} to your PATH, e.g.:
    echo 'export PATH=\"${dest}:\$PATH\"' >> ~/.profile && . ~/.profile" ;;
esac

if [ -x "${dest}/${BIN}" ]; then
  ok "$("${dest}/${BIN}" version 2>/dev/null || echo "run '${BIN} --help' to get started")"
fi
ok "done. Next: ${BOLD}${BIN} login${RESET}"
