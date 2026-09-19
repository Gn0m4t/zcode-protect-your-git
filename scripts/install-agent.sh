#!/bin/sh
set -eu

usage() {
  printf '%s\n' "Usage: $0 [--dry-run] <codex|claude|pi|all> --server <https://server.example.com>"
}

dry_run=0
target=
server_url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run)
      dry_run=1
      shift
      ;;
    --server)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      server_url=$2
      shift 2
      ;;
    codex|claude|pi|all)
      [ -z "$target" ] || { usage >&2; exit 2; }
      target=$1
      shift
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

case "$target" in
  codex|claude|pi|all) ;;
  *) usage >&2; exit 2 ;;
esac
[ -n "$server_url" ] || { printf '%s\n' 'The --server address is required.' >&2; usage >&2; exit 2; }

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
bin_dir=${ZPUG_BIN_DIR:-"$HOME/.local/bin"}
go_bin=${GO:-go}
build_root=$repo_root/.tmp-test
cd "$repo_root"

run() {
  if [ "$dry_run" -eq 1 ]; then
    printf '+ '
    printf "'%s' " "$@"
    printf '\n'
  else
    "$@"
  fi
}

need_command() {
  if [ "$dry_run" -eq 0 ] && ! command -v "$1" >/dev/null 2>&1; then
    printf 'Required command not found: %s\n' "$1" >&2
    exit 1
  fi
}

need_command "$go_bin"
run mkdir -p "$repo_root/bin" "$bin_dir" "$build_root/tmp" "$build_root/go-build" "$build_root/go-tmp" "$build_root/gopath" "$build_root/gobin"
run env TMPDIR="$build_root/tmp" GOCACHE="$build_root/go-build" GOTMPDIR="$build_root/go-tmp" GOPATH="$build_root/gopath" GOBIN="$build_root/gobin" "$go_bin" build -trimpath -o "$repo_root/bin/zcode-agent" ./cmd/zcode-agent
run cp "$repo_root/bin/zcode-agent" "$bin_dir/zcode-agent"
run chmod 0755 "$bin_dir/zcode-agent"

install_codex() {
  need_command codex
  run codex plugin marketplace add "$repo_root"
  run codex plugin add zcode-protect@zcode-protect-local
}

install_claude() {
  need_command claude
  run claude plugin marketplace add "$repo_root"
  run claude plugin install zcode-protect@zcode-protect-local
}

install_pi() {
  need_command pi
  run pi install "$repo_root/plugins/zcode-protect"
}

case "$target" in
  codex) install_codex ;;
  claude) install_claude ;;
  pi) install_pi ;;
  all) install_codex; install_claude; install_pi ;;
esac

if [ "$dry_run" -eq 1 ]; then
  run "$bin_dir/zcode-agent" setup --server "$server_url"
elif [ -r /dev/tty ]; then
  "$bin_dir/zcode-agent" setup --server "$server_url" </dev/tty
else
  printf '%s\n' 'Interactive setup requires a terminal so installation and server consent can be confirmed once.' >&2
  exit 1
fi

case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *)
    printf '\nInstalled zcode-agent in %s. Add it to PATH before starting the Agent host:\n' "$bin_dir"
    printf '  export PATH="%s:$PATH"\n' "$bin_dir"
    ;;
esac

printf '\nSetup complete. Restart the Agent host to load the plugin.\n'
