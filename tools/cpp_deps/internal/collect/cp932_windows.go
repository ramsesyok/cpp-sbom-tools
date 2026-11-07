package collect

import (
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var procMultiByteToWideChar = syscall.NewLazyDLL("kernel32.dll").NewProc("MultiByteToWideChar")

// decodeCP932 は Shift_JIS（日本語版 Windows のログ）を変換する。外部ライブラリを増やさないよう OS の機能を使う。
func decodeCP932(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	buf := make([]uint16, len(data))
	n, _, _ := procMultiByteToWideChar.Call(932, 0,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return strings.ToValidUTF8(string(data), "�")
	}
	return string(utf16.Decode(buf[:n]))
}
