#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 vX.Y.Z SHA256 OUTPUT/frost.rb" >&2
  exit 2
}

[[ $# -eq 3 ]] || usage

version=$1
sha256=$2
output=$3

[[ $version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
  echo "invalid release version: $version" >&2
  exit 2
}
[[ $sha256 =~ ^[0-9a-f]{64}$ ]] || {
  echo "invalid SHA-256: $sha256" >&2
  exit 2
}
[[ $(basename "$output") == frost.rb ]] || {
  echo "formula output must be named frost.rb" >&2
  exit 2
}
[[ -d $(dirname "$output") ]] || {
  echo "formula output directory does not exist: $(dirname "$output")" >&2
  exit 2
}

temporary=$(mktemp "$(dirname "$output")/.frost.rb.XXXXXX")
cleanup() {
  rm -f "$temporary"
}
trap cleanup EXIT

# TODO: add `license "..."` once the repository ships a LICENSE file;
# `brew audit --strict` in release-tap.sh will flag its absence until then.
cat >"$temporary" <<EOF
class Frost < Formula
  desc "Recommend a model and execution profile for a task written in plain language"
  homepage "https://github.com/marcus/frost"
  url "https://github.com/marcus/frost/archive/refs/tags/$version.tar.gz"
  sha256 "$sha256"
  head "https://github.com/marcus/frost.git", branch: "main"

  depends_on "go" => :build

  def install
    ENV["CGO_ENABLED"] = "0"
    ldflags = [
      "-s",
      "-w",
      "-X github.com/marcus/frost/pkg/buildinfo.Version=$version",
      "-X github.com/marcus/frost/pkg/buildinfo.Commit=homebrew",
    ].join(" ")
    system "go", "build", *std_go_args(output: bin/"frost", ldflags:), "./cmd/frost"
  end

  test do
    assert_match "frost $version (homebrew)", shell_output("#{bin}/frost version")
  end
end
EOF

ruby -c "$temporary" >/dev/null
chmod 0644 "$temporary"
mv "$temporary" "$output"
trap - EXIT

echo "rendered $output for $version"
