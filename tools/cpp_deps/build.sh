#!/bin/sh
# 配布用のバイナリを dist/ に作る。依存ライブラリの取得があるので、ネット接続PCで実行する。
# 配布物に添えるライセンス文と、各バイナリの CycloneDX SBOM も作る。
# SBOM 生成には Syft が必要。SYFT 環境変数で実行ファイルを指定できる。
set -e
cd "$(dirname "$0")"
export CGO_ENABLED=0
mkdir -p dist
rm -f dist/windows-system-inventory.ps1
syft=${SYFT:-syft}
export SYFT_CHECK_FOR_APP_UPDATE=false
if ! "$syft" version >/dev/null 2>&1; then
	echo "エラー: SBOM 作成には Syft が必要です。SYFT に実行ファイルを指定してください" >&2
	exit 1
fi
for cmd in cpp_deps cpp_sbom; do
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "dist/$cmd-linux-amd64" "./cmd/$cmd"
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o "dist/$cmd-linux-arm64" "./cmd/$cmd"
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "dist/$cmd-windows-amd64.exe" "./cmd/$cmd"
done

for cmd in cpp_deps cpp_sbom; do
	for target in linux-amd64 linux-arm64 windows-amd64.exe; do
		binary="dist/$cmd-$target"
		"$syft" "file:$binary" --override-default-catalogers go-module-binary-cataloger -o "cyclonedx-json=dist/$cmd-${target%.exe}.cdx.json"
	done
done

# バイナリに組み込まれるモジュールのライセンス文を集める（3つの OS 向けの和集合）
notice=dist/THIRD_PARTY_LICENSES.txt
: >"$notice"
add_license() { # add_license <名前> <フォルダ>
	found=
	for f in "$2"/LICENSE* "$2"/LICENCE* "$2"/COPYING*; do
		[ -f "$f" ] || continue
		found=1
		{
			echo "================================================================"
			echo "$1 ($(basename "$f"))"
			echo "================================================================"
			cat "$f"
			echo
		} >>"$notice"
	done
	if [ -z "$found" ]; then
		echo "ライセンス文が見つかりません: $1 ($2)" >&2
		exit 1
	fi
}
add_license "Go standard library" "$(go env GOROOT)"
for goos in linux windows; do
	GOOS=$goos go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}|{{.Dir}}{{end}}{{end}}' ./cmd/...
done | sort -u | while IFS='|' read -r name dir; do
	[ -n "$name" ] && add_license "$name" "$dir"
done
(cd dist && sha256sum cpp_deps-* cpp_sbom-* THIRD_PARTY_LICENSES.txt > SHA256SUMS)
ls -l dist
