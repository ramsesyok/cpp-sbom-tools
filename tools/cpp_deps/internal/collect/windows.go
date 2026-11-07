package collect

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	originMicrosoft = "Microsoft"
	originProject   = "プロジェクト内"
	originBundled   = "成果物に同梱"
	originExternal  = "別途導入"
	originUnknown   = "不明"
)

var (
	msPathPattern = regexp.MustCompile(`(?i)[\\/](Windows Kits|Microsoft Visual Studio|Microsoft SDKs)[\\/]`)
	msDLLPattern  = regexp.MustCompile(`^(api-ms-win-|ext-ms-win-|vcruntime|msvcp|msvcr|mfc|atl|ucrtbase|concrt|vcomp|vccorlib)`)

	// 英語版: "Note: including file:"  日本語版: "メモ: インクルード ファイル:"
	includeLine = regexp.MustCompile(`(?i)(including file:|インクルード ファイル:)\s*(.+)$`)
	nodePrefix  = regexp.MustCompile(`^\s*(\d+)>`)

	// 英語の言語パックがない Visual Studio では VSLANG=1033 が効かず、リンカの出力は日本語のままになる
	searchingLine   = regexp.MustCompile(`(?i)Searching (.+\.lib):\s*$`)
	searchingLineJa = regexp.MustCompile(`(?i)^\s*(?:\d+>)?\s*(.+\.lib) を検索中:\s*$`)
	finishedLine    = regexp.MustCompile(`(?i)Finished searching libraries|ライブラリの検索が終了しました`)
	foundLine       = regexp.MustCompile(`(?i)^\s*(\d+>)?\s*(Found |.+ が見つかりました)`)

	// MSVC 以外のコンパイラの手がかり。ログのコマンド行（x86_64-w64-mingw32-gcc.exe -c … など）と、実行時に読み込むDLL
	otherCompilerLine = regexp.MustCompile(`(?i)(?:^|[\s"'/\\])((?:[\w.+]+-)*(?:gcc|g\+\+|clang|clang\+\+|clang-cl)(?:-\d+)?(?:\.exe)?|(?:c\+\+|cc)\.exe)["']?\s+[-/]`)
	otherCompilerDLL  = regexp.MustCompile(`^(libgcc_s|libstdc\+\+|libwinpthread|libc\+\+|libunwind)`)
)

// findOtherCompilerInLog はビルドログから MSVC 以外のコンパイラのコマンドを探し、見つかった名前を返す。
func findOtherCompilerInLog(lines []string) string {
	for _, line := range lines {
		if m := otherCompilerLine.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// findOtherCompilerDLL は読み込むDLLの名前（小文字）から MinGW / clang のランタイムを探す。
func findOtherCompilerDLL(dlls []string) string {
	for _, name := range dlls {
		if otherCompilerDLL.MatchString(name) {
			return name
		}
	}
	return ""
}

// findMinGWBinary は MinGW のスタートアップコードが埋め込む文字列を持つ EXE/DLL を探し、ファイル名を返す。
func findMinGWBinary(paths []string) string {
	marker := []byte("Mingw-w64 runtime failure")
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil && bytes.Contains(data, marker) {
			return filepath.Base(p) + " に MinGW の印"
		}
	}
	return ""
}

// otherCompilerWarning は MSVC 以外と判断したときの注意文を返す。
func otherCompilerWarning(evidence string) string {
	return "MSVC 以外のコンパイラ（MinGW / clang など）でビルドされているようです（手がかり: " + evidence + "）。この手順の対象外で、ヘッダと静的ライブラリを調べられません。設定を変えてビルドし直しても解決しないので、手順書の管理者に相談してください"
}

// hasPathPrefix は path が root の配下かどうかを、大文字小文字と / \ の違いを無視して調べる。
func hasPathPrefix(path, root string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "/", `\`)) }
	path, root = norm(path), strings.TrimRight(norm(root), `\`)
	return root != "" && (path == root || strings.HasPrefix(path, root+`\`))
}

type headerGroup struct {
	dir, origin string
	count       int
}

type originRules struct {
	msRoots []string
	src     string
	bin     string
}

func newOriginRules(src, bin string) originRules {
	r := originRules{src: src, bin: bin}
	add := func(env, sub string) {
		if base := os.Getenv(env); strings.TrimSpace(base) != "" {
			r.msRoots = append(r.msRoots, base+sub)
		}
	}
	add("WINDIR", "")
	add("ProgramFiles(x86)", `\Windows Kits`)
	add("ProgramFiles", `\Windows Kits`)
	add("ProgramFiles(x86)", `\Microsoft Visual Studio`)
	add("ProgramFiles", `\Microsoft Visual Studio`)
	return r
}

func (r originRules) origin(path string) string {
	if path == "" {
		return originUnknown
	}
	if msPathPattern.MatchString(path) {
		return originMicrosoft
	}
	for _, root := range r.msRoots {
		if hasPathPrefix(path, root) {
			return originMicrosoft
		}
	}
	if hasPathPrefix(path, r.src) {
		return originProject
	}
	if hasPathPrefix(path, r.bin) {
		return originBundled
	}
	return originExternal
}

// decodeLog はログの文字コード（UTF-8 / Shift_JIS）を自動判別して行に分ける。
func decodeLog(data []byte) []string {
	text := string(data)
	if !utf8.Valid(data) {
		text = decodeCP932(data)
	}
	text = strings.TrimPrefix(text, "\uFEFF")
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

// parseIncludes は /showIncludes の出力からヘッダを取り出す（大文字小文字違いは1つにまとめる）。
func parseIncludes(lines []string) []string {
	var headers []string
	seen := map[string]bool{}
	for _, line := range lines {
		m := includeLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		p := strings.TrimSpace(m[2])
		key := strings.ToLower(p)
		if p == "" || seen[key] {
			continue
		}
		seen[key] = true
		headers = append(headers, p)
	}
	return headers
}

// parseLinkLibs は link /VERBOSE の出力から実際に使ったライブラリを取り出す。
// "Searching <ライブラリ>:" の後に "Found <シンボル>" があれば、そのライブラリを使った。
func parseLinkLibs(lines []string) (used []string, searched int) {
	current := map[string]string{} // 並列ビルドの行頭番号 "1>" ごとに、検索中のライブラリを覚える
	seen := map[string]bool{}
	for _, line := range lines {
		node := "0"
		if m := nodePrefix.FindStringSubmatch(line); m != nil {
			node = m[1]
		}
		m := searchingLine.FindStringSubmatch(line)
		if m == nil {
			m = searchingLineJa.FindStringSubmatch(line)
		}
		if m != nil {
			current[node] = strings.TrimSpace(m[1])
			searched++
		} else if finishedLine.MatchString(line) {
			delete(current, node)
		} else if lib, ok := current[node]; ok && foundLine.MatchString(line) {
			if key := strings.ToLower(lib); !seen[key] {
				seen[key] = true
				used = append(used, lib)
			}
		}
	}
	return used, searched
}

// projectFiles はプロジェクト内のファイルを小文字のファイル名で引けるようにしたもの。
type projectFiles map[string][]string

func indexProject(root string) projectFiles {
	idx := projectFiles{}
	if root == "" {
		return idx
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".svn" {
				return filepath.SkipDir
			}
			return nil
		}
		key := strings.ToLower(d.Name())
		idx[key] = append(idx[key], p)
		return nil
	})
	return idx
}

// resolve はログ内の相対パスを、末尾が一致するプロジェクト内のファイルに置き換える。
// 候補がちょうど1つのときだけ置き換え、それ以外はそのまま返す。
func (idx projectFiles) resolve(p string) string {
	tail := strings.ReplaceAll(p, "/", `\`)
	if strings.HasPrefix(tail, `\`) || (len(tail) >= 2 && tail[1] == ':') {
		return p
	}
	for strings.HasPrefix(tail, `.\`) || strings.HasPrefix(tail, `..\`) {
		tail = tail[strings.IndexByte(tail, '\\')+1:]
	}
	tail = strings.ToLower(tail)
	var hits []string
	for _, c := range idx[tail[strings.LastIndexByte(tail, '\\')+1:]] {
		if strings.HasSuffix(strings.ToLower(strings.ReplaceAll(c, "/", `\`)), `\`+tail) {
			hits = append(hits, c)
		}
	}
	if len(hits) == 1 {
		return hits[0]
	}
	return p
}

// resolveAll は相対パスを解決し、解決の結果同じになったものを1つにまとめる。
func (idx projectFiles) resolveAll(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		p = idx.resolve(p)
		if key := strings.ToLower(p); !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}
	return out
}

// parentDir はパスの親フォルダを文字列のまま返す（\.. などを畳まない）。
func parentDir(p string) string {
	i := strings.LastIndexAny(p, `\/`)
	if i < 0 {
		return ""
	}
	if i == 2 && p[1] == ':' {
		return p[:3]
	}
	return p[:i]
}

// sortRows は指定した2列の順に並べる（Sort-Object と同じく大文字小文字を区別しない）。
func sortRows(rows [][]string, col1, col2 int) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, c := range []int{col1, col2} {
			a, b := strings.ToLower(rows[i][c]), strings.ToLower(rows[j][c])
			if a != b {
				return a < b
			}
		}
		return false
	})
}

func listBinaries(root string) []string {
	var bins []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".exe" && ext != ".dll" {
			return nil
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			bins = append(bins, p)
		}
		return nil
	})
	return bins
}

func resolveDir(p string) (string, error) {
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(abs, `\`), nil
}
