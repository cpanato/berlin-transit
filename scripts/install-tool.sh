#!/bin/sh
# Install only reviewed tool versions; verify archives before extracting binaries.
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
tool=${1:?usage: sh scripts/install-tool.sh TOOL}
os=$(uname -s)
arch=$(uname -m)
case "$os/$arch" in
  Linux/x86_64) platform=linux_amd64; dash=linux-amd64; trivy_platform=Linux-64bit; leaks_platform=linux_x64 ;;
  Darwin/arm64) platform=darwin_arm64; dash=darwin-arm64; trivy_platform=macOS-ARM64; leaks_platform=darwin_arm64 ;;
  *) echo "Unsupported tool installation platform: $os/$arch" >&2; exit 1 ;;
esac

case "$tool" in
  golangci-lint)
    repo=golangci/golangci-lint; version=2.15.0
    archive="golangci-lint-$version-$dash.tar.gz"
    member="golangci-lint-$version-$dash/golangci-lint" ;;
  actionlint)
    repo=rhysd/actionlint; version=1.7.12
    archive="actionlint_${version}_$platform.tar.gz"; member=actionlint ;;
  trivy)
    repo=aquasecurity/trivy; version=0.76.1
    archive="trivy_${version}_$trivy_platform.tar.gz"; member=trivy ;;
  gitleaks)
    repo=gitleaks/gitleaks; version=8.30.1
    archive="gitleaks_${version}_$leaks_platform.tar.gz"; member=gitleaks ;;
  goreleaser)
    repo=goreleaser/goreleaser; version=2.19.0
    case "$os" in Linux) archive=goreleaser_Linux_x86_64.tar.gz ;; Darwin) archive=goreleaser_Darwin_arm64.tar.gz ;; esac
    member=goreleaser ;;
  ko)
    repo=ko-build/ko; version=0.19.1
    case "$os" in Linux) archive=ko_0.19.1_Linux_x86_64.tar.gz ;; Darwin) archive=ko_0.19.1_Darwin_arm64.tar.gz ;; esac
    member=ko ;;
  helm)
    version=4.3.1
    archive="helm-v$version-$dash.tar.gz"; member="$dash/helm" ;;
  *) echo "Unknown tool: $tool" >&2; exit 1 ;;
esac

expected=$(awk -v archive="$archive" '$2 == archive { print $1 }' "$root/scripts/tool-checksums.txt")
test "${#expected}" -eq 64
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
if test "$tool" = helm; then
  url="https://get.helm.sh/$archive"
else
  url="https://github.com/$repo/releases/download/v$version/$archive"
fi
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
  --connect-timeout 10 --max-time 180 --retry 3 \
  "$url" -o "$work/archive.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$work/archive.tar.gz" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$work/archive.tar.gz" | awk '{print $1}')
fi
if test "$actual" != "$expected"; then echo "Checksum mismatch for $archive" >&2; exit 1; fi
mkdir -p "$root/.tools/bin"
# Extract just the named executable to stdout, never arbitrary archive paths.
tar -xOf "$work/archive.tar.gz" "$member" > "$work/$tool"
chmod 755 "$work/$tool"
mv "$work/$tool" "$root/.tools/bin/$tool"
