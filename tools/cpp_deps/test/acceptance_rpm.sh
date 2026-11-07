#!/bin/bash
# rpm 系（Rocky Linux など）での確認。Python がなくても動くことも確かめられる。
# 使い方: acceptance_rpm.sh <dist フォルダ>
# gcc-c++ と zlib-devel が入っていること。
set -u
TOOL=$(realpath "$1")/cpp_deps-linux-amd64
WORK=$(mktemp -d)
FAILED=0

cat >"$WORK/main.cpp" <<'EOF'
#include <cstdio>
#include <zlib.h>
int main() { std::puts(zlibVersion()); return 0; }
EOF
mkdir "$WORK/build"
cd "$WORK"
g++ -Wl,--trace main.cpp -lz -o build/app >build_link.log 2>&1
cat >compile_commands.json <<EOF
[{"directory":"$WORK","file":"main.cpp","command":"g++ -c main.cpp -o main.o"}]
EOF
"$TOOL" --compdb compile_commands.json --linklog build_link.log --bin build --src . --out deps.json
check() { # check <説明> <文字列>
	if grep -qF -- "$2" deps.json; then echo "OK   $1"; else echo "NG   $1"; FAILED=1; fi
}
check "platform" '"platform": "linux"'
check "PURL が pkg:rpm" '"purl": "pkg:rpm/'
check "zlib-devel を検出" '"name": "zlib-devel"'
[ "$FAILED" = 0 ] && echo "すべて成功" || echo "失敗あり"
exit $FAILED
