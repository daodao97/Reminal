#!/bin/sh
# reminal installer — downloads the latest release and installs to ~/.local/bin.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/harshalgajjar/Reminal/main/install.sh | sh
#
# Env overrides:
#   REMINAL_VERSION       Install a specific version (default: latest)
#   REMINAL_INSTALL_DIR   Install location (default: ~/.local/bin)

set -e

REPO="harshalgajjar/Reminal"
INSTALL_DIR="${REMINAL_INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS.
case "$(uname -s)" in
    Darwin) OS="darwin" ;;
    Linux)  OS="linux" ;;
    *) echo "reminal: unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

# Detect architecture.
case "$(uname -m)" in
    arm64|aarch64)  ARCH="arm64" ;;
    x86_64|amd64)   ARCH="amd64" ;;
    *) echo "reminal: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

# Set up shell integration for the user's shell: put reminal on PATH (so a fresh
# terminal can just run `reminal`) and enable tab-completion for session ids and
# names. Best-effort — never fails the install. Skip with REMINAL_NO_RC=1 (the
# older REMINAL_NO_COMPLETION=1 is still honored). Idempotent and self-repairing:
# each run rewrites a single marker-guarded block, so re-running the installer
# fixes a broken PATH and upgrades never duplicate. The PATH line only prepends
# when missing; the completion line sources `reminal completion ...` at startup,
# so it stays fresh across upgrades.
setup_shell() {
    { [ "${REMINAL_NO_RC:-}" = "1" ] || [ "${REMINAL_NO_COMPLETION:-}" = "1" ]; } && return 0

    _begin="# >>> reminal >>>"
    _end="# <<< reminal <<<"

    # Prefer a $HOME-relative dir so the rc line is portable and readable.
    case "$INSTALL_DIR" in
        "$HOME/"*) _rc_dir="\$HOME/${INSTALL_DIR#"$HOME"/}" ;;
        *)         _rc_dir="$INSTALL_DIR" ;;
    esac
    # PATH snippet for POSIX shells: prepend only when not already present, so
    # it's safe to run on every shell startup.
    _path_snip="case \":\$PATH:\" in *\":${_rc_dir}:\"*) ;; *) export PATH=\"${_rc_dir}:\$PATH\" ;; esac"

    _add_to_rc() { # $1=rcfile  $2=body
        _rc="$1"
        _dir=$(dirname "$_rc")
        [ -d "$_dir" ] || mkdir -p "$_dir" 2>/dev/null || return 0
        if [ ! -e "$_rc" ]; then : >"$_rc" 2>/dev/null || return 0; fi
        # Strip any previously-managed reminal block (this marker or the older
        # "reminal completion" one) so we rewrite exactly one fresh block.
        if grep -qE '^# >>> reminal( completion)? >>>' "$_rc" 2>/dev/null; then
            _tmp=$(mktemp) 2>/dev/null || return 0
            awk '
              /^# >>> reminal( completion)? >>>/ { skip=1 }
              skip==0 { print }
              /^# <<< reminal( completion)? <<</ { skip=0 }
            ' "$_rc" >"$_tmp" 2>/dev/null && cat "$_tmp" >"$_rc" 2>/dev/null
            rm -f "$_tmp" 2>/dev/null
        fi
        printf '\n%s\n%s\n%s\n' "$_begin" "$2" "$_end" >>"$_rc" 2>/dev/null || return 0
        RC_UPDATED=1
        echo "  + reminal shell setup written to $_rc"
    }

    # OSC 7 cwd announcement: lets reminal's Dir column (and any modern
    # terminal) follow cd's as EVENTS instead of being polled — the shell
    # tells the terminal where it is. Percent-encodes the minimum that breaks
    # a file:// URI. Harmless anywhere: unknown OSC is ignored by terminals.
    _osc7_fn='__reminal_osc7() {
  _p=$(printf "%s" "$PWD" | sed "s/%/%25/g; s/ /%20/g; s/#/%23/g")
  printf "\033]7;file://%s%s\007" "${HOST:-$(hostname 2>/dev/null)}" "$_p"
}'

    case "$(basename "${SHELL:-sh}")" in
        zsh)
            _add_to_rc "${ZDOTDIR:-$HOME}/.zshrc" "$_path_snip
if command -v reminal >/dev/null 2>&1; then
  (( \$+functions[compdef] )) || { autoload -Uz compinit && compinit -u; }
  source <(reminal completion zsh)
fi
$_osc7_fn
autoload -Uz add-zsh-hook 2>/dev/null && { add-zsh-hook chpwd __reminal_osc7; __reminal_osc7; }"
            ;;
        bash)
            _rcfile="$HOME/.bashrc"
            [ "$OS" = "darwin" ] && [ -e "$HOME/.bash_profile" ] && _rcfile="$HOME/.bash_profile"
            _add_to_rc "$_rcfile" "$_path_snip
command -v reminal >/dev/null 2>&1 && source <(reminal completion bash)
$_osc7_fn
case \";\$PROMPT_COMMAND;\" in *\";__reminal_osc7;\"*) ;; *) PROMPT_COMMAND=\"__reminal_osc7\${PROMPT_COMMAND:+;\$PROMPT_COMMAND}\" ;; esac"
            ;;
        fish)
            _fdir="${XDG_CONFIG_HOME:-$HOME/.config}/fish/completions"
            if mkdir -p "$_fdir" 2>/dev/null && printf '%s\n' 'reminal completion fish | source' >"$_fdir/reminal.fish" 2>/dev/null; then
                echo "  + tab-completion installed: $_fdir/reminal.fish"
            fi
            # fish uses its own PATH syntax; add a guarded block to config.fish.
            _add_to_rc "${XDG_CONFIG_HOME:-$HOME/.config}/fish/config.fish" "if not contains \"${_rc_dir}\" \$PATH
    set -gx PATH \"${_rc_dir}\" \$PATH
end"
            ;;
        *)
            echo "  (couldn't detect your shell — add ${INSTALL_DIR} to PATH and run: reminal completion <bash|zsh|fish>)"
            ;;
    esac
}

# Re-run shell setup only (no download): REMINAL_SETUP_COMPLETION_ONLY=1.
if [ "${REMINAL_SETUP_COMPLETION_ONLY:-}" = "1" ]; then
    setup_shell || true
    exit 0
fi

# curl is required; check upfront for a clearer error.
if ! command -v curl >/dev/null 2>&1; then
    echo "reminal: curl is required to install. Install curl and try again." >&2
    exit 1
fi

# Resolve the latest version from the redirect of /releases/latest so we don't
# need a GitHub API token. The effective URL ends in /releases/tag/vX.Y.Z.
VERSION="$REMINAL_VERSION"
if [ -z "$VERSION" ]; then
    EFFECTIVE=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
    VERSION="${EFFECTIVE##*/v}"
fi
if [ -z "$VERSION" ]; then
    echo "reminal: failed to resolve latest version" >&2
    exit 1
fi

TARBALL="reminal_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/v${VERSION}/${TARBALL}"

echo "Installing reminal v${VERSION} (${OS}/${ARCH})..."

# Stage in a temp dir; cleaned up on exit.
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

curl -fsSL -o "$TMPDIR/$TARBALL" "$URL"
tar -xzf "$TMPDIR/$TARBALL" -C "$TMPDIR"

mkdir -p "$INSTALL_DIR"

# macOS ships reminal.app — a signed bundle holding the CLI, the ScreenCaptureKit
# helper, and the icon under ONE code identity, so a single Screen Recording grant
# also covers the background daemon (the "+" sessions). Install the bundle to
# ~/Applications and symlink the CLI onto PATH so `reminal` works exactly as
# before. Linux (and any older bare-binary release) installs the plain binary.
APP_DIR="${REMINAL_APP_DIR:-$HOME/Applications}"
if [ "$OS" = "darwin" ] && [ -d "$TMPDIR/reminal.app" ]; then
    mkdir -p "$APP_DIR"
    rm -rf "$APP_DIR/reminal.app"
    mv "$TMPDIR/reminal.app" "$APP_DIR/reminal.app"
    xattr -dr com.apple.quarantine "$APP_DIR/reminal.app" 2>/dev/null || true
    ln -sf "$APP_DIR/reminal.app/Contents/MacOS/reminal" "$INSTALL_DIR/reminal"
    # Register with LaunchServices so Finder/Settings show the icon.
    _lsr="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
    [ -x "$_lsr" ] && "$_lsr" -f "$APP_DIR/reminal.app" >/dev/null 2>&1 || true
    INSTALLED_DESC="${APP_DIR}/reminal.app (CLI: ${INSTALL_DIR}/reminal)"
else
    mv "$TMPDIR/reminal" "$INSTALL_DIR/reminal"
    chmod +x "$INSTALL_DIR/reminal"
    # macOS: a binary downloaded via curl is not quarantined by Gatekeeper (only
    # downloads from browsers/Mail/etc. get the com.apple.quarantine xattr), but
    # strip it defensively in case a future install method re-introduces it.
    [ "$OS" = "darwin" ] && xattr -d com.apple.quarantine "$INSTALL_DIR/reminal" 2>/dev/null || true
    INSTALLED_DESC="${INSTALL_DIR}/reminal"
fi

echo
echo "Installed reminal v${VERSION} to ${INSTALLED_DESC}"

# macOS bundle post-install:
if [ "$OS" = "darwin" ] && [ -d "$APP_DIR/reminal.app" ]; then
    # Always run the background daemon. It's the single process that performs all
    # screen capture + input injection, so ONE grant to reminal.app (via
    # `reminal permissions`) covers every session — terminal or "+". `daemon
    # --install` writes the LaunchAgent pointing at the bundle binary and starts it
    # (idempotent; on upgrade it re-points + restarts).
    "$INSTALL_DIR/reminal" daemon --install >/dev/null 2>&1 || true
    # Stale loose helper from a previous bare install (the bundle carries its own).
    rm -f "$INSTALL_DIR/reminal-capture" 2>/dev/null || true
    echo
    echo "To mirror + control windows, grant reminal its permissions once:"
    echo "  reminal permissions"
fi

# Set up shell integration: PATH + tab-completion (best-effort).
setup_shell || true

# Heads-up if a different reminal is going to win on PATH.
EXISTING="$(command -v reminal 2>/dev/null || true)"
if [ -n "$EXISTING" ] && [ "$EXISTING" != "$INSTALL_DIR/reminal" ]; then
    echo
    echo "Note: another reminal is already on your PATH at: $EXISTING"
    echo "      It will take precedence. To remove a brew install: brew uninstall reminal"
fi

# Tell the user how to actually run it.
echo
case ":$PATH:" in
    *":$INSTALL_DIR:"*)
        echo "Run: reminal"
        ;;
    *)
        if [ -n "${RC_UPDATED:-}" ]; then
            # We added INSTALL_DIR to the rc, but the current shell hasn't
            # re-read it — so `reminal` won't resolve here until a new shell.
            echo "Added $INSTALL_DIR to your PATH. Open a new terminal and run:"
            echo "  reminal"
            echo
            echo "Or use it right now in this shell:"
            echo "  export PATH=\"$INSTALL_DIR:\$PATH\" && reminal"
        else
            echo "$INSTALL_DIR is not on your PATH. Add this to your shell rc:"
            echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
            echo
            echo "Or run directly:"
            echo "  $INSTALL_DIR/reminal"
        fi
        ;;
esac
