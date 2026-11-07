// cpp_deps はビルドで使った部品を調べて deps.json を作ります。ビルドPCで実行します。
// 外部への通信は一切行いません。仕様は docs/cpp_deps_spec.md を参照。
package main

import (
	"os"

	"cpp_deps/internal/collect"
	"cpp_deps/internal/inventory"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "inventory" {
		os.Exit(inventory.Run(os.Args[2:]))
	}
	os.Exit(collect.Run(os.Args[1:]))
}
