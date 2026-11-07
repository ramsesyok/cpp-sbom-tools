#!/bin/bash
# docs/cpp_deps_spec.md と docs/ledger_spec.md の受け入れテスト（Ubuntu 24.04）。
# 使い方: acceptance_ubuntu.sh <リポジトリのフォルダ> <dist フォルダ>
# build-essential cmake bear libpaho-mqtt-dev libpaho-mqttpp-dev が入っていること。
# Python と Go は不要。環境変数 SCANCODE に scancode の実行ファイルを指定すると、ScanCode も実物で動かす。
# 環境変数 SYFT に syft の実行ファイルを指定すると、ライセンスを実物から取る。
set -u
REPO=$(realpath "$1")
DIST=$(realpath "$2")
TOOL=$DIST/cpp_deps-linux-amd64
SBOM=$DIST/cpp_sbom-linux-amd64
WORK=$(mktemp -d)
FAILED=0

ok() { echo "OK   $1"; }
ng() { echo "NG   $1"; FAILED=1; }
expect() { # expect <説明> <ファイル> <文字列>
	if grep -qF -- "$3" "$2"; then ok "$1"; else ng "$1"; grep -n "" "$2" | head -20; fi
}
# JSON の配列の要素数を数える代わりに、deps.json の整形に合わせて行を数える
count() { # count <deps.json> <reason>
	grep -cF "\"reason\": \"$2\"" "$1"
}
section_count() { # section_count <deps.json> <項目名>。その配列の直下の要素数
	awk -v key="\"$2\": [" 'index($0,key)==3 { if ($0 ~ /\]/) {print 0; exit} on=1; next }
		on && /^  \]/ { print n+0; exit } on && /^    \{/ { n++ }' "$1"
}

cp -r "$REPO/samples/mqtt-client-cmake" "$REPO/samples/mqtt-client-make" "$WORK/"

echo "=== 1. CMake・動的リンク ==="
P=$WORK/mqtt-client-cmake
cmake -S "$P" -B "$P/build" -DCMAKE_EXPORT_COMPILE_COMMANDS=ON \
	-DCMAKE_EXE_LINKER_FLAGS="-Wl,--trace" -DCMAKE_SHARED_LINKER_FLAGS="-Wl,--trace" >/dev/null
cmake --build "$P/build" --clean-first >"$P/build_link.log" 2>&1
(cd "$P" && "$TOOL" --compdb build/compile_commands.json --linklog build_link.log --bin build --src . --out "$WORK/cmake.json" >"$WORK/cmake.txt" 2>&1) || ng "cpp_deps の実行"
n=$(section_count "$WORK/cmake.json" packages)
[ "$n" = 13 ] && ok "OSパッケージ 13 個" || ng "OSパッケージが $n 個"
n=$(count "$WORK/cmake.json" project_folder)
[ "$n" = 2 ] && ok "project_folder 2 件" || ng "project_folder が $n 件"
n=$(section_count "$WORK/cmake.json" items)
[ "$n" = 2 ] && ok "items は合計 2 件" || ng "items が合計 $n 件"
expect "src のフォルダ" "$WORK/cmake.json" '"rel_path": "src"'
expect "third_party/nlohmann のフォルダ" "$WORK/cmake.json" '"rel_path": "third_party/nlohmann"'
expect "bin_rel" "$WORK/cmake.json" '"bin_rel": "build"'
expect "distro" "$WORK/cmake.json" '"distro": "ubuntu-24.04"'
expect "画面の結果" "$WORK/cmake.txt" '人の判断が必要なもの: 2 件'

echo "=== 2. Make＋bear・静的リンク ==="
P=$WORK/mqtt-client-make
(cd "$P" && bear -- make CC="gcc -Wl,--trace" CXX="g++ -Wl,--trace" >build_link.log 2>&1)
(cd "$P" && "$TOOL" --compdb compile_commands.json --linklog build_link.log --bin build --src . --out "$WORK/make.json" >"$WORK/make.txt" 2>&1) || ng "cpp_deps の実行"
grep -A3 '"kind": "静的ライブラリ"' "$WORK/make.json" | grep -q 'libpaho-mqtt3c.a' && ok "libpaho-mqtt3c.a が静的ライブラリ" || ng "libpaho-mqtt3c.a の種別"
grep -A2 'libpaho-mqtt3c.a' "$WORK/make.json" | grep -q '"origin": "OSパッケージ"' && ok "libpaho-mqtt3c.a が OSパッケージ" || ng "libpaho-mqtt3c.a の出どころ"
grep -A4 '"name": "libpaho-mqtt-dev' "$WORK/make.json" | grep -q '"usage": "ヘッダとライブラリ"' && ok "libpaho-mqtt-dev がヘッダとライブラリ" || ng "libpaho-mqtt-dev の使用形態"

echo "=== 3. 同梱の静的ライブラリ（相対パス）と 5. 場所を特定できない ==="
P=$WORK/vend
mkdir -p "$P/vend/lib" "$P/vend/include"
echo 'int foo(void);' >"$P/vend/include/foo.h"
echo 'int foo(void) { return 1; }' >"$P/foo.c"
printf '#include "foo.h"\nint main(void) { return foo(); }\n' >"$P/main.c"
(
	cd "$P"
	gcc -c foo.c -o foo.o && ar rcs vend/lib/libfoo.a foo.o && rm foo.o
	gcc -Ivend/include -c main.c -o main.o
	mkdir build
	gcc -Wl,--trace main.o -L vend/lib -lfoo -o build/app >build_link.log 2>&1
	cat >compile_commands.json <<EOF
[{"directory":"$P","file":"main.c","command":"gcc -Ivend/include -c main.c -o main.o"},
 {"directory":"/nonexistent","file":"x.c","arguments":["gcc","-c","x.c"]}]
EOF
	"$TOOL" --compdb compile_commands.json --linklog build_link.log --bin build --src . --out "$WORK/vend.json" >"$WORK/vend.txt" 2>&1
	# プロジェクトの外のフォルダでリンクした場合：--linkdir がないと場所を特定できない
	mkdir -p "$WORK/outside/lib"
	: >"$WORK/outside/lib/libbar.a"
	printf 'lib/libbar.a\nnowhere/libgone.so.1\n' >other.log
	"$TOOL" --linklog other.log --src . --out "$WORK/other1.json" >"$WORK/other1.txt" 2>&1
	"$TOOL" --linklog other.log --linkdir "$WORK/outside" --src . --out "$WORK/other2.json" >"$WORK/other2.txt" 2>&1
)
expect "project_library" "$WORK/vend.json" '"reason": "project_library"'
expect "再実行できない命令の注意" "$WORK/vend.txt" '注意: 1 件のコンパイル命令は再実行できませんでした'
expect "unresolved" "$WORK/other2.json" '"reason": "unresolved"'
expect "unresolved の画面の注意" "$WORK/other2.txt" '注意: 場所を特定できないライブラリが 1 件あります'
[ "$(count "$WORK/other1.json" unresolved)" = 2 ] && ok "--linkdir なしでは 2 件が unresolved" || ng "--linkdir なしの unresolved"
[ "$(count "$WORK/other2.json" unresolved)" = 1 ] && ok "--linkdir で 1 件解消" || ng "--linkdir で解消しない"

echo "=== 4. 注意メッセージ ==="
: >"$WORK/empty.log"
(cd "$WORK" && "$TOOL" --linklog empty.log --src . --out empty.json >empty.txt 2>&1)
expect "空のログの注意" "$WORK/empty.txt" '注意: ビルドログにリンクの記録がありません'
P=$WORK/mqtt-client-cmake
(cd "$P" && "$TOOL" --compdb build/compile_commands.json --src . --out "$WORK/nolink.json" >"$WORK/nolink.txt" 2>&1)
expect "--linklog なしの注意" "$WORK/nolink.txt" '注意: --linklog が指定されていません'
expect "ヘッダだけの使用形態" "$WORK/nolink.json" '"usage": "ヘッダ"'
"$TOOL" >/dev/null 2>&1; [ $? = 2 ] && ok "引数なしは終了コード 2" || ng "引数なしの終了コード"

echo "=== 台帳から SBOM まで ==="
P=$WORK/mqtt-client-cmake
SCAN=$WORK/scancode.json
if [ -n "${SCANCODE:-}" ]; then
	(cd "$WORK" && "$SCANCODE" -clpi -n 2 --json-pp "$SCAN" "$P" >"$WORK/scancode.txt" 2>&1) || ng "ScanCode の実行"
	ok "ScanCode は実物を使用"
else
	# ScanCode の結果の形だけを再現する（testdata の third_party の結果を流用）
	cat >"$SCAN" <<'EOF'
{"files":[
 {"path":"mqtt-client-cmake","type":"directory"},
 {"path":"mqtt-client-cmake/src/main.cpp","type":"file","detected_license_expression_spdx":null,"holders":[]},
 {"path":"mqtt-client-cmake/third_party/nlohmann/json.hpp","type":"file","detected_license_expression_spdx":"MIT AND Apache-2.0","holders":[{"holder":"Niels Lohmann"},{"holder":"The Abseil Authors"}]},
 {"path":"mqtt-client-cmake/third_party/nlohmann/LICENSE.MIT","type":"file","detected_license_expression_spdx":"MIT","holders":[{"holder":"Niels Lohmann"}]}
]}
EOF
fi
if [ -n "${SYFT:-}" ]; then
	"$SYFT" scan dir:/ --select-catalogers "+dpkg-db-cataloger,+rpm-db-cataloger" -o cyclonedx-json="$WORK/env.json" >/dev/null 2>&1 || ng "Syft の実行"
elif [ -f "$REPO/testdata/linux-cmake/downstream/build-env.cdx.json.gz" ]; then
	gzip -dc "$REPO/testdata/linux-cmake/downstream/build-env.cdx.json.gz" >"$WORK/env.json"
else
	echo "エラー: SYFT を指定してください（検証データ testdata は git に含めていません）" >&2
	exit 2
fi
"$SBOM" ledger --deps "$WORK/cmake.json" --scan "$SCAN" --src "$P" --osenv "$WORK/env.json" --out "$WORK/台帳.xlsx" >"$WORK/ledger.txt" 2>&1 && ok "ledger の実行" || { ng "ledger の実行"; cat "$WORK/ledger.txt"; }
expect "ledger の画面の結果" "$WORK/ledger.txt" '完了しました。'
"$SBOM" ledger --deps "$WORK/cmake.json" --scan "$SCAN" --src "$P" --osenv "$WORK/env.json" --out "$WORK/台帳.xlsx" >"$WORK/ledger2.txt" 2>&1
[ $? = 1 ] && ok "既存の台帳は上書きしない" || ng "既存の台帳を上書きした"
"$SBOM" convert --ledger "$WORK/台帳.xlsx" --name app --version 1.0 --out "$WORK/x.cdx.json" >"$WORK/cdx.txt" 2>&1
[ $? = 1 ] && ok "未記入の台帳は変換しない" || ng "未記入の台帳を変換した"
expect "未記入の理由" "$WORK/cdx.txt" '区分が未選択です'
[ ! -e "$WORK/x.cdx.json" ] && ok "未記入では SBOM を作らない" || ng "未記入で SBOM ができた"
"$SBOM" check --ledger "$WORK/台帳.xlsx" >"$WORK/check.txt" 2>&1
[ $? = 1 ] && ok "check は未記入の台帳を完成と扱わない" || ng "check の終了コード"
expect "check の未記入の理由" "$WORK/check.txt" '区分が未選択です'
"$SBOM" check >/dev/null 2>&1; [ $? = 2 ] && ok "check の引数なしは終了コード 2" || ng "check の引数なしの終了コード"

# 結果の確認用に台帳と deps.json を残す
if [ -n "${KEEP_DIR:-}" ]; then
	cp "$WORK/台帳.xlsx" "$WORK/cmake.json" "$WORK/make.json" "$KEEP_DIR/"
	[ -f "$SCAN" ] && cp "$SCAN" "$KEEP_DIR/scancode.json"
	cp "$WORK/env.json" "$KEEP_DIR/env.json"
fi

echo
[ "$FAILED" = 0 ] && echo "すべて成功" || echo "失敗あり"
exit $FAILED
