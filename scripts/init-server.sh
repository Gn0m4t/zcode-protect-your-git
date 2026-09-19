#!/bin/sh
set -eu

mode=${1:-cloud}
output=${2:-configs/server.local.json}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

case "$output" in
  /*) output_path=$output ;;
  *) output_path=$repo_root/$output ;;
esac

case "$mode" in
  cloud) template=$repo_root/configs/server.example.json ;;
  development|dev) template=$repo_root/configs/server.dev.json ;;
  *)
    printf 'Usage: %s <cloud|development> [output.json]\n' "$0" >&2
    exit 2
    ;;
esac

if [ -e "$output_path" ]; then
  printf 'Refusing to overwrite existing config: %s\n' "$output_path" >&2
  exit 1
fi

mkdir -p "$(dirname -- "$output_path")"
cp "$template" "$output_path"
chmod 0600 "$output_path"

printf 'Created %s server config: %s\n' "$mode" "$output_path"
if [ "$mode" = "cloud" ]; then
  printf '%s\n' 'Next: replace the example URL/AccessKey ID, provide the callback public key, then export:'
  printf '%s\n' "  export ZPUG_OSS_ACCESS_KEY_SECRET='...'"
fi
printf 'Run: make server CONFIG=%s\n' "$output_path"
