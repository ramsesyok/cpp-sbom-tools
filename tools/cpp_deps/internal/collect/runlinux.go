package collect

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cpp_deps/internal/deps"
)

func runLinux(args []string) int {
	fset := flag.NewFlagSet("cpp_deps", flag.ContinueOnError)
	compdb := fset.String("compdb", "", "bearで作った compile_commands.json")
	linklog := fset.String("linklog", "", "-Wl,--trace を付けてビルドしたときのログ")
	linkdir := fset.String("linkdir", "", "リンクを実行したフォルダ（ログ内の相対パスの基準。既定は --src）")
	binDir := fset.String("bin", "", "成果物（実行ファイル・.so）があるフォルダ")
	src := fset.String("src", "", "プロジェクトのフォルダ（必須）")
	out := fset.String("out", "deps.json", "出力ファイル")
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
	if *compdb == "" && *linklog == "" && *binDir == "" {
		fmt.Fprintln(os.Stderr, "エラー: --compdb / --linklog / --bin の少なくとも1つを指定してください")
		return 2
	}
	if *src == "" {
		fmt.Fprintln(os.Stderr, "エラー: --src（プロジェクトのフォルダ）を指定してください")
		return 2
	}
	if !exists(*src) {
		return fail("プロジェクトのフォルダが見つかりません: %s", *src)
	}

	osText, _ := os.ReadFile("/etc/os-release")
	osinfo := parseOSRelease(string(osText))
	db := newPkgDB()
	if db.kind == "" {
		fmt.Fprintln(os.Stderr, "警告: dpkg も rpm も見つかりません。パッケージ逆引きはできません。")
	}

	srcRoot := realPath(*src)
	files := map[string]string{} // パス -> 種別
	var unresolvedLibs []string
	var warnings []string

	if *compdb != "" {
		data, err := os.ReadFile(*compdb)
		if err != nil {
			return fail("%s を読めません: %v", *compdb, err)
		}
		var entries []compileEntry
		if err := json.Unmarshal([]byte(strings.TrimPrefix(string(data), "\uFEFF")), &entries); err != nil {
			return fail("%s の形式が正しくありません: %v", *compdb, err)
		}
		fmt.Printf("[1/4] コンパイル命令 %d 件を調べています…\n", len(entries))
		errCount := 0
		for _, e := range entries {
			got, ok := headersFromEntry(e)
			if !ok {
				errCount++
				continue
			}
			dir := e.Directory
			if dir == "" {
				dir = "."
			}
			self := e.File
			if !filepath.IsAbs(self) {
				self = filepath.Join(dir, self)
			}
			self = realPath(self)
			for _, p := range got {
				if isSourceFile(p) && realPath(p) == self {
					continue
				}
				p = filepath.Clean(p)
				if _, dup := files[p]; !dup {
					files[p] = "ヘッダ"
				}
			}
		}
		if errCount > 0 {
			warnings = append(warnings, fmt.Sprintf("%d 件のコンパイル命令は再実行できませんでした（結果に含まれません）", errCount))
		}
	}

	if *linklog != "" {
		fmt.Println("[2/4] リンクで使ったライブラリをビルドログから調べています…")
		cwd, _ := os.Getwd()
		found, unresolved, err := libsFromLinkLog(*linklog, []string{*linkdir, *src, cwd}, indexLibs(srcRoot))
		if err != nil {
			return fail("%s を読めません: %v", *linklog, err)
		}
		for _, p := range found {
			files[p] = libKind(p)
		}
		if len(found) == 0 {
			warnings = append(warnings, "ビルドログにリンクの記録がありません。手順のとおり -Wl,--trace を付けてビルドし直してください")
		}
		sort.Strings(unresolved)
		for i, u := range unresolved {
			if i == 0 || u != unresolved[i-1] {
				unresolvedLibs = append(unresolvedLibs, u)
			}
		}
	} else if *compdb != "" {
		warnings = append(warnings, "--linklog が指定されていません。静的ライブラリ（.a）は調べていません")
	}

	if *binDir != "" {
		fmt.Println("[3/4] 成果物の共有ライブラリを調べています…")
		filepath.WalkDir(*binDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			st, err := os.Stat(p)
			if err != nil || !st.Mode().IsRegular() || !isELF(p) {
				return nil
			}
			rc, stdout := runCmd("", "ldd", p)
			if rc != 0 {
				return nil
			}
			for _, lib := range parseLdd(stdout) {
				lib = filepath.Clean(lib)
				if _, dup := files[lib]; !dup {
					files[lib] = "共有ライブラリ"
				}
			}
			return nil
		})
	}

	fmt.Printf("[4/4] %d 個のファイルの持ち主パッケージを調べています…\n", len(files))
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	result := &deps.Result{Platform: deps.PlatformLinux, Distro: distroName(osinfo), Src: srcRoot}
	if *binDir != "" {
		result.BinRel = binRel(srcRoot, realPath(*binDir))
	}
	pkgs := map[string]*pkgSummary{}
	projHdrDirs := map[string]int{}
	extHdrDirs := map[string]int{}
	srcUse := map[string]map[string]bool{} // ソースパッケージ -> {ヘッダ, ライブラリ}
	for _, p := range paths {
		kind := files[p]
		rp := realPath(p)
		if rel := relUnder(srcRoot, rp); rel != "" {
			result.Files = append(result.Files, deps.File{Kind: kind, Path: p, Origin: originProject})
			if kind == "ヘッダ" {
				projHdrDirs[filepath.Dir(p)]++
			} else {
				result.Items = append(result.Items, deps.Item{Reason: deps.ReasonProjectLibrary, Kind: kind, Path: p, RelPath: rel, FileCount: 1})
			}
			continue
		}
		var info pkgInfo
		if db.kind != "" {
			info = db.owner(p)
		}
		if info.name == "" {
			result.Files = append(result.Files, deps.File{Kind: kind, Path: p, Origin: "パッケージ外"})
			if kind == "ヘッダ" {
				extHdrDirs[filepath.Dir(p)]++
			} else {
				result.Items = append(result.Items, deps.Item{Reason: deps.ReasonExternal, Kind: kind, Path: p, FileCount: 1})
			}
			continue
		}
		purl := makePURL(db.kind, osinfo, info)
		result.Files = append(result.Files, deps.File{Kind: kind, Path: p, Origin: "OSパッケージ", Package: info.name, Version: info.version, PURL: purl})
		source := info.source
		if source == "" {
			source = info.name
		}
		s := pkgs[info.name]
		if s == nil {
			s = &pkgSummary{}
			pkgs[info.name] = s
		}
		s.version, s.source, s.purl = info.version, source, purl
		s.files++
		if srcUse[source] == nil {
			srcUse[source] = map[string]bool{}
		}
		if kind == "ヘッダ" {
			srcUse[source]["ヘッダ"] = true
		} else {
			srcUse[source]["ライブラリ"] = true
		}
	}

	for d, n := range projHdrDirs {
		result.Items = append(result.Items, deps.Item{Reason: deps.ReasonProjectFolder, Kind: fmt.Sprintf("ヘッダ（%d個）", n), Path: d, RelPath: relUnder(srcRoot, realPath(d)), FileCount: n})
	}
	for d, n := range extHdrDirs {
		result.Items = append(result.Items, deps.Item{Reason: deps.ReasonExternal, Kind: fmt.Sprintf("ヘッダ（%d個）", n), Path: d, FileCount: n})
	}
	for _, u := range unresolvedLibs {
		result.Items = append(result.Items, deps.Item{Reason: deps.ReasonUnresolved, Kind: "静的/共有ライブラリ", Path: u, FileCount: 1})
	}

	names := make([]string, 0, len(pkgs))
	for n := range pkgs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := pkgs[n]
		result.Packages = append(result.Packages, deps.Package{
			Name: n, Version: s.version, Source: s.source,
			Usage:     useForm(srcUse[s.source], *linklog != "" || *binDir != ""),
			FileCount: s.files, PURL: s.purl,
		})
	}
	return finish(*out, result, warnings)
}

// distroName は /etc/os-release の内容から <ID>-<VERSION_ID> を作る。
func distroName(osinfo map[string]string) string {
	id := osinfo["ID"]
	if id == "" {
		id = "linux"
	}
	return id + "-" + osinfo["VERSION_ID"]
}
