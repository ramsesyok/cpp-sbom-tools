//go:build !windows

package collect

import "strings"

// Linux 版はこの処理を通らない（Windows 版のログ解析をテストするときのためだけにある）。
func decodeCP932(data []byte) string {
	return strings.ToValidUTF8(string(data), "�")
}
