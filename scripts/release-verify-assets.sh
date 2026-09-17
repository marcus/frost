#!/usr/bin/env bash
set -euo pipefail

dist=${1:-dist}
release_version=${2:-}

[[ -d $dist ]] || {
  echo "release directory does not exist: $dist" >&2
  exit 1
}
[[ -f $dist/checksums.txt ]] || {
  echo "missing $dist/checksums.txt" >&2
  exit 1
}
if [[ -n $release_version && ! $release_version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "release version must be strict SemVer vX.Y.Z" >&2
  exit 2
fi

temporary=$(mktemp -d)
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT

find "$dist" -mindepth 1 -maxdepth 1 -type f -name '*.tar.gz' |
  LC_ALL=C sort >"$temporary/archives"
archive_count=$(wc -l <"$temporary/archives" | tr -d ' ')
[[ $archive_count -eq 4 ]] || {
  echo "expected 4 release archives, found $archive_count" >&2
  exit 1
}

archive_names=$(tr '[:upper:]' '[:lower:]' <"$temporary/archives")
for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
  grep -Eq "_${target}\.tar\.gz$" <<<"$archive_names" || {
    echo "missing archive for $target" >&2
    exit 1
  }
done

while IFS= read -r archive; do
  basename "$archive"
done <"$temporary/archives" | LC_ALL=C sort >"$temporary/archive-basenames"
awk '
  NF != 2 { exit 2 }
  { name=$2; sub(/^\*/, "", name); print name }
' "$dist/checksums.txt" | LC_ALL=C sort >"$temporary/checksum-basenames" || {
  echo "checksums.txt must contain one checksum and filename per line" >&2
  exit 1
}
if ! cmp -s "$temporary/archive-basenames" "$temporary/checksum-basenames"; then
  echo "checksums.txt entries do not exactly match the four release archives" >&2
  diff -u "$temporary/archive-basenames" "$temporary/checksum-basenames" >&2 || true
  exit 1
fi

host_os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64) host_arch=amd64 ;;
  arm64 | aarch64) host_arch=arm64 ;;
  *) host_arch=unsupported ;;
esac

index=0
while IFS= read -r archive; do
  index=$((index + 1))
  unpack="$temporary/unpack-$index"
  mkdir "$unpack"
  tar -xzf "$archive" -C "$unpack"

  find "$unpack" -mindepth 1 -maxdepth 1 >"$temporary/top-level-$index"
  [[ $(wc -l <"$temporary/top-level-$index" | tr -d ' ') -eq 1 ]] || {
    echo "$archive must contain exactly one top-level entry" >&2
    exit 1
  }
  root=$(head -n 1 "$temporary/top-level-$index")
  [[ -d $root && ! -L $root ]] || {
    echo "$archive's only top-level entry must be a directory" >&2
    exit 1
  }

  [[ -x $root/frost ]] || {
    echo "$archive is missing executable frost" >&2
    exit 1
  }
  for required in README.md CHANGELOG.md; do
    [[ -f $root/$required ]] || {
      echo "$archive is missing $required" >&2
      exit 1
    }
  done

  archive_lower=$(printf '%s' "$archive" | tr '[:upper:]' '[:lower:]')
  if [[ -n $release_version && $archive_lower == *"_${host_os}_${host_arch}.tar.gz" ]]; then
    output=$($root/frost version)
    [[ $output == "frost $release_version ("* ]] || {
      echo "$archive has an unexpected frost version: $output" >&2
      exit 1
    }
  fi
done <"$temporary/archives"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$dist" && sha256sum --check checksums.txt)
else
  (cd "$dist" && shasum -a 256 --check checksums.txt)
fi

echo "verified checksums and all four release archives"
