#!/usr/bin/env bash
# Build CLI + desktop app for the current OS (or a given target) into dist/.
#   scripts/build.sh            # host platform
#   scripts/build.sh windows    # windows/amd64 + arm64 (cross build from mac/linux)
#   scripts/build.sh cli        # CLI only, every platform
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
LD="-s -w -X main.version=$VERSION"
mkdir -p dist

cli() { # os arch out
  CGO_ENABLED=0 GOOS=$1 GOARCH=$2 go build -trimpath -ldflags "$LD" -o "$3" ./cmd/mndl
}

mac() {
  export MACOSX_DEPLOYMENT_TARGET=${MACOSX_DEPLOYMENT_TARGET:-11.0}
  wails build -clean -trimpath -platform darwin/universal -ldflags "$LD"
  APP="build/bin/MultiNet Downloader.app"
  cli darwin arm64 /tmp/mndl-arm64
  cli darwin amd64 /tmp/mndl-amd64
  lipo -create -output "$APP/Contents/MacOS/mndl" /tmp/mndl-arm64 /tmp/mndl-amd64
  cp "$APP/Contents/MacOS/mndl" dist/mndl-darwin-universal
  codesign --force --deep -s - "$APP" >/dev/null 2>&1 || true
  rm -f dist/MultiNet-Downloader-macos.zip
  ditto -c -k --keepParent "$APP" dist/MultiNet-Downloader-macos.zip
  if command -v hdiutil >/dev/null; then
    rm -rf /tmp/mndl-dmg && mkdir -p /tmp/mndl-dmg && cp -R "$APP" /tmp/mndl-dmg/ && ln -s /Applications /tmp/mndl-dmg/Applications
    rm -f dist/MultiNet-Downloader-macos.dmg
    hdiutil create -quiet -volname "MultiNet Downloader" -srcfolder /tmp/mndl-dmg -ov -format UDZO dist/MultiNet-Downloader-macos.dmg
  fi
}

win() {
  for arch in amd64 arm64; do
    wails build -clean -trimpath -platform windows/$arch -ldflags "$LD -H windowsgui" -webview2 embed
    out="/tmp/mndl-win-$arch"; rm -rf "$out"; mkdir -p "$out"
    cp "build/bin/MultiNet Downloader.exe" "$out/"
    cli windows $arch "$out/mndl.exe"
    cp "$out/mndl.exe" dist/mndl-windows-$arch.exe
    rm -f dist/MultiNet-Downloader-windows-$arch.zip
    (cd "$out" && zip -q -r "$OLDPWD/dist/MultiNet-Downloader-windows-$arch.zip" .)
  done
}

linux() {
  arch=$(go env GOARCH)
  wails build -clean -trimpath -platform linux/$arch -ldflags "$LD" ${WAILS_TAGS:+-tags "$WAILS_TAGS"}
  out="/tmp/mndl-linux-$arch"; rm -rf "$out"; mkdir -p "$out"
  cp "build/bin/MultiNet Downloader" "$out/multinet-downloader"
  cli linux $arch "$out/mndl"
  cp "$out/mndl" dist/mndl-linux-$arch
  tar -C "$out" -czf dist/MultiNet-Downloader-linux-$arch.tar.gz .
}

all_cli() {
  for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
    os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=.exe
    cli "$os" "$arch" "dist/mndl-$os-$arch$ext"
  done
}

case "${1:-host}" in
  host) case "$(uname -s)" in Darwin) mac ;; Linux) linux ;; *) win ;; esac ;;
  mac|darwin) mac ;;
  windows|win) win ;;
  linux) linux ;;
  cli) all_cli ;;
  *) echo "usage: $0 [host|mac|windows|linux|cli]"; exit 2 ;;
esac
ls -la dist
