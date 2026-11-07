package tocdx

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"cpp_deps/internal/ledger"
)

// RunCheck は cpp_sbom check の本体。台帳を SBOM に変換できる状態かを確かめ、終了コードを返す。
// 0 = 完成、1 = 足りない項目がある（または読めない）、2 = 指定の誤り。何も書き出さない。
func RunCheck(args []string) int {
	fset := flag.NewFlagSet("cpp_sbom check", flag.ContinueOnError)
	ledgerPath := fset.String("ledger", "", "部品台帳の Excel（必須）")
	cvebinPath := fset.String("cvebin", "", "作業4の cve-bin-tool の結果。見つかった部品が台帳にあるかも確かめる")
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fset.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "エラー: 不明な引数です: %s\n", strings.Join(fset.Args(), " "))
		return 2
	}
	if *ledgerPath == "" {
		fmt.Fprintln(os.Stderr, "エラー: --ledger を指定してください")
		return 2
	}
	var parts []cvebinPart
	if *cvebinPath != "" {
		var err error
		if parts, err = readCvebin(*cvebinPath); err != nil {
			fmt.Fprintf(os.Stderr, "エラー: %s を読めません: %v\n", *cvebinPath, err)
			return 1
		}
	}
	if excelLockExists(*ledgerPath) {
		fmt.Println("注意: 台帳が Excel で開かれているようです。保存していない変更は読めません")
	}
	rows, err := ledger.Read(*ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %s を読めません: %v\n", *ledgerPath, err)
		return 1
	}
	_, libraries, warnings, errs, excluded := inspect(rows, false)
	// 直す機会を1回で済ませるため、未完成のときも注意をあわせて出す。
	for _, w := range warnings {
		fmt.Println("注意: " + w)
	}
	unresolved := 0
	if *cvebinPath != "" {
		unresolved = reportCvebin(parts, rows)
	}
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "エラー: 台帳はまだ完成していません。次の %d 件を直してから、もう一度確かめてください\n", len(errs))
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  "+e)
		}
		return 1
	}
	if unresolved > 0 {
		fmt.Fprintf(os.Stderr, "エラー: 台帳はまだ完成していません。作業4の部品 %d 件を確認してから、もう一度確かめてください\n", unresolved)
		return 1
	}
	fmt.Printf("台帳は完成しています。SBOM に入る部品は %d 件です。", libraries)
	if excluded > 0 {
		fmt.Printf("「対象外」は %d 行です。", excluded)
	}
	fmt.Println()
	return 0
}
