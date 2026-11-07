// cpp_deps はビルドで使った部品を調べて deps.json を作ります。ビルドPCで実行します。
// 外部への通信は一切行いません。仕様は docs/cpp_deps_spec.md を参照。
package main

import (
	"os"

	"cpp_deps/internal/collect"
)

func main() {
	os.Exit(collect.Run(os.Args[1:]))
}
