// cpp_sbom は部品台帳の Excel を作り（ledger）、その完成を確かめ（check）、SBOM に変換します（convert）。
// ビルドPCから持ち出した結果を使い、別のPCで実行できます。
// 外部への通信は一切行いません。仕様は docs/ledger_spec.md を参照。
package main

import (
	"fmt"
	"os"

	"cpp_deps/internal/makeledger"
	"cpp_deps/internal/tocdx"
)

const usage = `使い方:
  cpp_sbom ledger  ...  deps.json と ScanCode の結果から部品台帳.xlsx を作る
  cpp_sbom check   ...  記入した部品台帳.xlsx が完成しているかを確かめる
  cpp_sbom convert ...  完成した部品台帳.xlsx を CycloneDX の SBOM に変換する

それぞれの指定項目は cpp_sbom ledger -h のように確認できます。`

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--version", "version":
		fmt.Println("cpp_sbom", version)
	case "ledger":
		os.Exit(makeledger.Run(os.Args[2:]))
	case "check":
		os.Exit(tocdx.RunCheck(os.Args[2:]))
	case "convert":
		os.Exit(tocdx.Run(os.Args[2:]))
	case "-h", "--help", "help":
		fmt.Println(usage)
	default:
		fmt.Fprintf(os.Stderr, "エラー: 不明なサブコマンドです: %s\n%s\n", os.Args[1], usage)
		os.Exit(2)
	}
}
