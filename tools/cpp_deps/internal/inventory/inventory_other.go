//go:build !windows

package inventory

import "fmt"

func Run(args []string) int {
	fmt.Println("エラー: inventory は Windows の納入構成で実行してください")
	return 2
}
