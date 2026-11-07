// Package collect は cpp_deps の本体です。
//
// ビルドで実際に使われたヘッダとライブラリを洗い出し、出どころを調べて deps.json に出力します。
// Python・PowerShell・Visual Studio がなくても動きます。外部への通信は一切行いません。
//
// Windows 向けにビルドすると Windows 版、それ以外では Linux 版の動きをします。
// 仕様は docs/cpp_deps_spec.md を参照。
package collect

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"cpp_deps/internal/deps"
)

// Run は収集を実行し、終了コードを返す。
func Run(args []string) int {
	if runtime.GOOS == "windows" {
		return runWindows(args)
	}
	return runLinux(args)
}

// runCmd は外部コマンドを実行し、終了コードと標準出力（改行は \n に統一）を返す。起動できなければ 127。
func runCmd(dir, name string, args ...string) (int, string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, normalizeNewlines(stdout.String())
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), normalizeNewlines(stdout.String())
	}
	return 127, ""
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "エラー: "+format+"\n", a...)
	return 1
}

// relUnder は path が root の配下なら root からの相対パス（区切りは /）を返す。配下でなければ空。
func relUnder(root, path string) string {
	if root == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// binRel は成果物フォルダの --src からの相対パスを返す。--src の外か、--src と同じなら空。
func binRel(src, bin string) string {
	if bin == "" {
		return ""
	}
	rel := relUnder(src, bin)
	if rel == "." {
		return ""
	}
	return rel
}

// finish は deps.json を書き、結果を画面に出す。
func finish(out string, r *deps.Result, warnings []string) int {
	unresolved := 0
	for _, it := range r.Items {
		if it.Reason == deps.ReasonUnresolved {
			unresolved++
		}
	}
	if unresolved > 0 {
		warnings = append(warnings, fmt.Sprintf("場所を特定できないライブラリが %d 件あります。--linkdir にリンクを実行したフォルダを指定して、ここで再実行してください", unresolved))
	}
	r.Warnings = warnings
	deps.SortItems(r.Items)
	if dir := filepath.Dir(out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fail("出力先のフォルダを作れません: %v", err)
		}
	}
	if err := deps.Write(out, r); err != nil {
		return fail("%s を書き込めません: %v", out, err)
	}
	for _, m := range warnings {
		fmt.Println("注意: " + m)
	}
	abs, _ := filepath.Abs(out)
	fmt.Println("完了しました。")
	if r.Platform == deps.PlatformLinux {
		fmt.Printf("  OSパッケージ: %d 個\n", len(r.Packages))
	} else {
		fmt.Printf("  EXE/DLL: %d 個\n", len(r.Binaries))
	}
	fmt.Printf("  人の判断が必要なもの: %d 件\n", len(r.Items))
	fmt.Printf("  結果: %s\n", abs)
	return 0
}
