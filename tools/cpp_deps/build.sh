#!/bin/sh
# 配布用のバイナリを dist/ に作る。依存ライブラリの取得があるので、ネット接続PCで実行する。
# 配布物に添えるライセンス文（THIRD_PARTY_LICENSES.txt）も作る。
set -e
cd "$(dirname "$0")"
export CGO_ENABLED=0
mkdir -p dist
for cmd in cpp_deps cpp_sbom; do
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "dist/$cmd-linux-amd64" "./cmd/$cmd"
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o "dist/$cmd-linux-arm64" "./cmd/$cmd"
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "dist/$cmd-windows-amd64.exe" "./cmd/$cmd"
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
ls -l dist
