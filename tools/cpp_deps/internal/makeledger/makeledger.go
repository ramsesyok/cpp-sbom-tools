// Package makeledger は cpp_sbom の ledger サブコマンドです。
// deps.json と ScanCode の結果から部品台帳の Excel を作ります。仕様は docs/ledger_spec.md を参照。
package makeledger

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"cpp_deps/internal/deps"
	"cpp_deps/internal/ledger"
)

// 「やること」の文言
const (
	todoLicense   = "ライセンスを記入"
	todoToolchain = "バージョンを開発担当者に確認して記入"
	todoFolder    = "自社で書いたコードだけなら区分を「対象外」に。OSSを含むなら「同梱」にして色の付いたセルを記入"
	todoLibrary   = "自社開発（OSSを含まない）なら区分を「対象外」に。OSSを含むなら自社でビルドしたものも「同梱」にして色の付いたセルを記入"
	todoExternal  = "部品名・バージョンを開発担当者に確認して記入"
	todoAsk       = "開発担当者に入手元を確認して記入"
	todoLinkdir   = "ビルドPCで --linkdir を付けて再実行しても残ったもの。開発担当者に確認し、区分を選んで記入"
)

// Run は ledger サブコマンドを実行し、終了コードを返す。
func Run(args []string) int {
	fset := flag.NewFlagSet("cpp_sbom ledger", flag.ContinueOnError)
	depsPath := fset.String("deps", "", "collect で作った deps.json（必須）")
	product := fset.String("product", "", "製品種別: software または system（必須）")
	delivery := fset.String("delivery", "", "実際に納入するファイルのフォルダ（必須）")
	systemInventory := fset.String("system-inventory", "", "完成した納入構成の CycloneDX JSON（system では必須）")
	scanPath := fset.String("scan", "", "ScanCode の --json-pp の結果（必須）")
	src := fset.String("src", "", "ScanCode で調べたプロジェクトのフォルダ（必須。バージョン候補を読む）")
	osenv := fset.String("osenv", "", "Syft で作った build-env.cdx.json（Linux では必須）")
	out := fset.String("out", "部品台帳.xlsx", "出力ファイル")
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
	if *depsPath == "" || *scanPath == "" || *src == "" || *delivery == "" || (*product != ledger.ScopeSoftware && *product != ledger.ScopeSystem) {
		fmt.Fprintln(os.Stderr, "エラー: --product software|system、--delivery、--deps、--scan、--src を指定してください")
		return 2
	}
	if *product == ledger.ScopeSystem && *systemInventory == "" {
		fmt.Fprintln(os.Stderr, "エラー: システム製品には --system-inventory を指定してください")
		return 2
	}
	if *product == ledger.ScopeSoftware && *systemInventory != "" {
		fmt.Fprintln(os.Stderr, "エラー: --system-inventory はシステム製品だけに指定してください")
		return 2
	}
	if st, err := os.Stat(*delivery); err != nil || !st.IsDir() {
		return fail("納入ファイルのフォルダが見つかりません: %s", *delivery)
	}
	if _, err := os.Stat(*out); err == nil {
		return fail("%s はすでにあります。記入した内容を守るため、上書きしません。作り直すときは、別の名前を --out に指定するか、古いファイルを移動してください", *out)
	}
	result, err := deps.Read(*depsPath)
	if err != nil {
		return fail("%s を読めません: %v", *depsPath, err)
	}
	if result.Platform == deps.PlatformLinux && *osenv == "" {
		fmt.Fprintln(os.Stderr, "エラー: Linux の結果には --osenv（build-env.cdx.json）を指定してください")
		return 2
	}
	var licenses map[string]string
	if result.Platform == deps.PlatformLinux {
		if licenses, err = readOSLicenses(*osenv); err != nil {
			return fail("%s を読めません: %v", *osenv, err)
		}
	}
	fold := foldFor(result.Platform)
	scan, err := readScan(*scanPath, fold)
	if err != nil {
		return fail("%s を読めません: %v", *scanPath, err)
	}
	if !scanMatches(result, scan, fold) {
		return fail("ScanCode で調べたフォルダと --src が違います。プロジェクトのフォルダ全体を ScanCode にかけ直してください")
	}
	for _, w := range result.Warnings {
		fmt.Println("注意: " + w)
	}

	delivered := deliveryNames(*delivery)
	if len(delivered) == 0 {
		return fail("--delivery に納入ファイルがありません: %s", *delivery)
	}
	rows := buildRows(result, scan, *src, licenses)
	rows = productRows(rows, result, delivered)
	if *product == ledger.ScopeSystem {
		installed, err := readSystemInventory(*systemInventory)
		if err != nil {
			return fail("%s を読めません: %v", *systemInventory, err)
		}
		rows = append(rows, installed...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return len(ledger.Missing(rows[i])) > 0 && len(ledger.Missing(rows[j])) == 0
	})
	if dir := filepath.Dir(*out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fail("出力先のフォルダを作れません: %v", err)
		}
	}
	if err := ledger.WriteWithScope(*out, *product, rows, findings(result)); err != nil {
		return fail("%s を書き込めません: %v", *out, err)
	}
	todo := 0
	for _, r := range rows {
		if len(ledger.Missing(r)) > 0 {
			todo++
		}
	}
	fmt.Printf("完了しました。部品 %d 行を %s に書き出しました（記入が必要な行 %d 行）。\n", len(rows), *out, todo)
	return 0
}

func deliveryNames(root string) map[string]bool {
	names := map[string]bool{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names[strings.ToLower(d.Name())] = true
		}
		return nil
	})
	return names
}

// productRows はビルド環境の情報を納入物の部品と区別する。
// ヘッダだけのパッケージはコードを取り込んだ可能性があるので人の確認へ回す。
func productRows(rows []ledger.Row, r *deps.Result, delivered map[string]bool) []ledger.Row {
	static, shipped := map[string]bool{}, map[string]bool{}
	for _, f := range r.Files {
		if f.Package == "" {
			continue
		}
		if f.Kind == "静的ライブラリ" {
			static[f.Package] = true
		}
		base := path.Base(strings.ReplaceAll(f.Path, `\`, "/"))
		if delivered[strings.ToLower(base)] && f.Kind != "ヘッダ" {
			shipped[f.Package] = true
		}
	}
	var out []ledger.Row
	for _, row := range rows {
		location := strings.ToLower(path.Base(strings.ReplaceAll(row.Get(ledger.ColLocation), `\`, "/")))
		if location != "" && !delivered[location] {
			// 動的ライブラリは納入物にない限り実行環境側の要件として扱う。
			if strings.Contains(location, ".so") || strings.HasSuffix(location, ".dll") {
				continue
			}
		}
		switch row.Get(ledger.ColKind) {
		case ledger.KindMicrosoft:
			continue // ビルド時のツールチェーン。納入側にあれば system-inventory から入る
		case ledger.KindOSPackage:
			var pkgName string
			for _, p := range r.Packages {
				if p.PURL == row.Get(ledger.ColPURL) {
					pkgName = p.Name
					break
				}
			}
			if static[pkgName] || shipped[pkgName] {
				row[ledger.ColKind] = ledger.KindBundled
				row[ledger.ColTodo] = "納入物に取り込まれた部品。版とライセンスを確認"
			} else if strings.Contains(row.Get(ledger.ColNote), "ヘッダのみ") {
				row[ledger.ColKind] = ""
				row[ledger.ColTodo] = "ヘッダのコードが製品に取り込まれたか確認。取り込まれたら同梱、そうでなければ対象外"
			} else {
				continue // 納入していない動的ライブラリ。調査結果には残す
			}
		}
		out = append(out, row)
	}
	return out
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "エラー: "+format+"\n", a...)
	return 1
}

// foldFor はパスを比べるときの変換を返す。Windows では大文字小文字を区別しない。
func foldFor(platform string) func(string) string {
	if platform == deps.PlatformWindows {
		return strings.ToLower
	}
	return func(s string) string { return s }
}

// scanMatches は project_folder のフォルダが ScanCode の結果に1つでもあるかを調べる。
func scanMatches(r *deps.Result, scan map[string]*folderScan, fold func(string) string) bool {
	any := false
	for _, it := range r.Items {
		if it.Reason != deps.ReasonProjectFolder || it.RelPath == "" {
			continue
		}
		any = true
		if _, ok := scan[fold(it.RelPath)]; ok {
			return true
		}
	}
	return !any
}

// readOSLicenses は Syft の結果から パッケージ名 → ライセンス を作る。
func readOSLicenses(file string) (map[string]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var bom struct {
		Components []struct {
			Name     string `json:"name"`
			Licenses []struct {
				Expression string `json:"expression"`
				License    struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"license"`
			} `json:"licenses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &bom); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, c := range bom.Components {
		var names []string
		seen := map[string]bool{}
		for _, l := range c.Licenses {
			n := l.Expression
			if n == "" {
				n = l.License.ID
			}
			if n == "" {
				n = l.License.Name
			}
			if n != "" && !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
		if len(names) > 0 {
			out[c.Name] = strings.Join(names, " AND ")
		}
	}
	return out, nil
}

func buildRows(r *deps.Result, scan map[string]*folderScan, src string, licenses map[string]string) []ledger.Row {
	fold := foldFor(r.Platform)
	var blankKind, external, microsoft, packages []ledger.Row

	for _, p := range r.Packages {
		name, _, _ := strings.Cut(p.Name, ":")
		lic := licenses[name]
		var note []string
		if p.Usage == "ヘッダのみ" {
			note = append(note, "ヘッダのみ")
		}
		row := ledger.Row{ledger.ColKind: ledger.KindOSPackage, ledger.ColName: name, ledger.ColVersion: p.Version, ledger.ColPURL: p.PURL}
		if lic != "" {
			row[ledger.ColLicense] = lic
			note = append(note, "ライセンスはOSのcopyrightファイルから自動記入")
		} else {
			row[ledger.ColTodo] = todoLicense
		}
		row[ledger.ColNote] = strings.Join(note, "、")
		packages = append(packages, row)
	}

	blankKind = append(blankKind, folderRows(r, scan, src, fold)...)

	for _, it := range r.Items {
		row := ledger.Row{ledger.ColLocation: it.Path}
		switch it.Reason {
		case deps.ReasonProjectLibrary:
			row[ledger.ColLocation] = displayRel(it)
			row[ledger.ColTodo] = todoLibrary
			blankKind = append(blankKind, row)
		case deps.ReasonBundledDLL:
			row[ledger.ColLocation] = displayRel(it)
			row[ledger.ColName] = it.ProductName
			row[ledger.ColVersion] = it.ProductVersion
			row[ledger.ColTodo] = todoLibrary
			blankKind = append(blankKind, row)
		case deps.ReasonUnresolved:
			row[ledger.ColTodo] = todoLinkdir
			blankKind = append(blankKind, row)
		case deps.ReasonExternal:
			row[ledger.ColKind] = ledger.KindExternal
			row[ledger.ColTodo] = todoExternal
			external = append(external, row)
		case deps.ReasonUnknownDLL, deps.ReasonUnreadableBinary:
			row[ledger.ColKind] = ledger.KindExternal
			row[ledger.ColTodo] = todoAsk
			external = append(external, row)
		}
	}

	if r.Platform == deps.PlatformWindows {
		versions := map[string]string{}
		for _, t := range r.Toolchain {
			versions[t.Name] = t.Version
		}
		for _, name := range []string{"MSVC", "Windows SDK"} {
			row := ledger.Row{ledger.ColKind: ledger.KindMicrosoft, ledger.ColName: name, ledger.ColVersion: versions[name]}
			if versions[name] == "" {
				row[ledger.ColTodo] = todoToolchain
			}
			microsoft = append(microsoft, row)
		}
	}

	sortByLocation(blankKind)
	sortByLocation(external)
	var rows []ledger.Row
	rows = append(rows, blankKind...)
	rows = append(rows, external...)
	rows = append(rows, microsoft...)
	rows = append(rows, packages...)
	return rows
}

func sortByLocation(rows []ledger.Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		return strings.ToLower(rows[i][ledger.ColLocation]) < strings.ToLower(rows[j][ledger.ColLocation])
	})
}

func displayRel(it deps.Item) string {
	if it.RelPath != "" {
		return it.RelPath
	}
	return it.Path
}

// folderCandidate は台帳の行の候補になるプロジェクト内のフォルダ。
type folderCandidate struct {
	rel  string
	scan *folderScan
}

// folderRows はプロジェクト内のフォルダの行を作る。
func folderRows(r *deps.Result, scan map[string]*folderScan, src string, fold func(string) string) []ledger.Row {
	candidates := map[string]*folderCandidate{}
	emptyScan := func(rel string) *folderScan { return &folderScan{rel: rel, holders: map[string]int{}} }
	for _, it := range r.Items {
		if it.Reason != deps.ReasonProjectFolder || it.RelPath == "" {
			continue
		}
		key := fold(it.RelPath)
		if candidates[key] != nil {
			continue
		}
		s := scan[key]
		if s == nil {
			s = emptyScan(it.RelPath)
		}
		candidates[key] = &folderCandidate{rel: it.RelPath, scan: s}
	}
	binKey := fold(r.BinRel)
	for key, s := range scan {
		if candidates[key] != nil || !s.detected() || excludedFolder(key, binKey) {
			continue
		}
		candidates[key] = &folderCandidate{rel: s.rel, scan: s}
	}

	// 親から順に、ライセンスと著作権者が完全に一致する子を親の行にまとめる
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if a, b := depth(keys[i]), depth(keys[j]); a != b {
			return a < b
		}
		return keys[i] < keys[j]
	})
	rep := map[string]string{} // フォルダ -> まとめ先
	members := map[string][]string{}
	for _, key := range keys {
		rep[key] = key
		parent := path.Dir(key)
		if parent == key || candidates[parent] == nil {
			continue
		}
		a, okA := candidates[key].scan.signature()
		b, okB := candidates[parent].scan.signature()
		if okA && okB && a == b {
			rep[key] = rep[parent]
			members[rep[parent]] = append(members[rep[parent]], key)
		}
	}

	rootName := lastElement(r.Src)
	var rows []ledger.Row
	for _, key := range keys {
		if rep[key] != key {
			continue
		}
		c := candidates[key]
		merged := emptyScan(c.rel)
		merged.merge(c.scan)
		hints := &hintCounter{}
		addVersionHints(hints, filepath.Join(src, filepath.FromSlash(c.rel)))
		for _, m := range members[key] {
			merged.merge(candidates[m].scan)
			addVersionHints(hints, filepath.Join(src, filepath.FromSlash(candidates[m].rel)))
		}

		name := path.Base(c.rel)
		location := c.rel
		if c.rel == "." {
			name = rootName
			location = "（プロジェクトの直下）"
		}
		if n := len(members[key]); n > 0 {
			location += fmt.Sprintf("（配下の %d フォルダを含む）", n)
		}
		top := hints.top(3)
		row := ledger.Row{
			ledger.ColName:     name,
			ledger.ColLicense:  ledger.JoinLicenses(merged.licenses),
			ledger.ColLocation: location,
			ledger.ColHolders:  strings.Join(merged.topHolders(5), " / "),
			ledger.ColHints:    strings.Join(top, " / "),
			ledger.ColTodo:     todoFolder,
		}
		if len(top) > 0 {
			if m := firstVersion.FindStringSubmatch(top[0]); m != nil {
				row[ledger.ColVersion] = m[1]
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func depth(rel string) int {
	if rel == "." {
		return 0
	}
	return strings.Count(rel, "/") + 1
}

// excludedFolder はライセンス検出だけで行を作らないフォルダ（.git、.svn、成果物の配下）を判定する。
func excludedFolder(rel, bin string) bool {
	for _, part := range strings.Split(rel, "/") {
		if part == ".git" || part == ".svn" {
			return true
		}
	}
	return bin != "" && (rel == bin || strings.HasPrefix(rel, bin+"/"))
}

func lastElement(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// findings はシート「調査結果」の行を作る。
func findings(r *deps.Result) [][]string {
	var rows [][]string
	for _, f := range r.Files {
		rows = append(rows, []string{f.Kind, f.Path, f.Origin, f.Package, f.Version, f.PURL, "", ""})
	}
	for _, b := range r.Binaries {
		rows = append(rows, []string{"EXE/DLL", b.File, "成果物", b.ProductName, b.ProductVersion, "", b.Company, b.Copyright})
	}
	return rows
}
