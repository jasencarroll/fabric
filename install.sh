#!/bin/sh
# Fabric installer
# Usage: curl -fsSL https://raw.githubusercontent.com/jasencarroll/fabric-server/main/install.sh | sh
set -e

REPO="jasencarroll/fabric-server"
INSTALL_DIR="${FABRIC_INSTALL_DIR:-$HOME/.local/bin}"

main() {
  os=$(uname -s)
  arch=$(uname -m)

  case "$os" in
    Darwin)  platform="Darwin" ;;
    Linux)   platform="Linux" ;;
    MINGW*|MSYS*|CYGWIN*) platform="Windows" ;;
    *)       echo "error: unsupported OS: $os"; exit 1 ;;
  esac

  case "$arch" in
    x86_64|amd64)  arch_name="x86_64" ;;
    arm64|aarch64) arch_name="arm64" ;;
    *)             echo "error: unsupported architecture: $arch"; exit 1 ;;
  esac

  if [ "$platform" = "Windows" ]; then
    binary="fabric-Windows-${arch_name}.exe"
  else
    binary="fabric-${platform}-${arch_name}"
  fi

  url="https://github.com/${REPO}/releases/latest/download/${binary}"

  echo "  fabric installer"
  echo "  os:      $platform"
  echo "  arch:    $arch_name"
  echo "  install: $INSTALL_DIR/fabric"
  echo ""

  mkdir -p "$INSTALL_DIR"

  echo "  downloading $binary..."
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$INSTALL_DIR/fabric"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$INSTALL_DIR/fabric" "$url"
  else
    echo "error: curl or wget required"
    exit 1
  fi

  chmod +x "$INSTALL_DIR/fabric"
  echo "  installed to $INSTALL_DIR/fabric"

  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
      echo ""
      echo "  add to your PATH:"
      shell_name=$(basename "$SHELL" 2>/dev/null || echo "sh")
      case "$shell_name" in
        zsh)  echo "    echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc && source ~/.zshrc" ;;
        bash) echo "    echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc && source ~/.bashrc" ;;
        fish) echo "    fish_add_path $INSTALL_DIR" ;;
        *)    echo "    export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
      esac
      ;;
  esac

  echo ""
  echo "  done! run 'fabric version' to verify."
}

main
