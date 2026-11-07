package collect

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"cpp_deps/internal/deps"
)

var (
	msvcPath = regexp.MustCompile(`(?i)[\\/]VC[\\/]Tools[\\/]MSVC[\\/]([^\\/]+)[\\/]`)
	sdkPath  = regexp.MustCompile(`(?i)[\\/]Windows Kits[\\/]10[\\/]Include[\\/]([^\\/]+)[\\/]`)
)

// mostFrequent はパスの一覧から正規表現の1番目の組を取り出し、最も多く現れたものを返す。
// 同数なら文字列として大きいもの（新しい版）を選ぶ。
func mostFrequent(paths []string, re *regexp.Regexp) string {
	counts := map[string]int{}
	for _, p := range paths {
		if m := re.FindStringSubmatch(p); m != nil {
			counts[m[1]]++
		}
	}
	best, bestN := "", 0
	for v, n := range counts {
		if n > bestN || (n == bestN && v > best) {
			best, bestN = v, n
		}
	}
	return best
}

// toolchainFromHeaders はヘッダのパスから MSVC と Windows SDK の版を読み取る。
func toolchainFromHeaders(headers []string) []deps.Tool {
	var tools []deps.Tool
	if v := mostFrequent(headers, msvcPath); v != "" {
		tools = append(tools, deps.Tool{Name: "MSVC", Version: v})
	}
	if v := mostFrequent(headers, sdkPath); v != "" {
		tools = append(tools, deps.Tool{Name: "Windows SDK", Version: v})
	}
	return tools
}

func runWindows(args []string) int {
	fset := flag.NewFlagSet("cpp_deps", flag.ContinueOnError)
	binDir := fset.String("bin", "", "成果物（EXE・DLL）があるフォルダ（必須）")
	srcDir := fset.String("src", "", "プロジェクトのフォルダ（必須）")
	buildLog := fset.String("buildlog", "", "/showIncludes と /VERBOSE を付けてビルドしたときのログ")
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
	if *binDir == "" {
		fmt.Fprintln(os.Stderr, "エラー: --bin（成果物のフォルダ）を指定してください")
		return 2
	}
	if *srcDir == "" {
		fmt.Fprintln(os.Stderr, "エラー: --src（プロジェクトのフォルダ）を指定してください")
		return 2
	}
	binFull, err := resolveDir(*binDir)
	if err != nil {
		return fail("成果物のフォルダが見つかりません: %v", err)
	}
	srcFull, err := resolveDir(*srcDir)
	if err != nil {
		return fail("プロジェクトのフォルダが見つかりません: %v", err)
	}
	rules := newOriginRules(srcFull, binFull)
	result := &deps.Result{Platform: deps.PlatformWindows, Src: srcFull, BinRel: binRel(srcFull, binFull)}
	var warnings []string

	// ---------- (1) 成果物の EXE / DLL ----------
	fmt.Println("[1/4] 成果物フォルダの EXE / DLL を調べています…")
	bins := listBinaries(binFull)
	localDLLs := map[string]string{}
	for _, b := range bins {
		if strings.EqualFold(filepath.Ext(b), ".dll") {
			localDLLs[strings.ToLower(filepath.Base(b))] = b
		}
	}
	imports := map[string][]string{}
	versions := map[string]versionInfo{}
	for _, b := range bins {
		rel, _ := filepath.Rel(binFull, b)
		info, err := readPE(b)
		if err != nil {
			result.Items = append(result.Items, deps.Item{Reason: deps.ReasonUnreadableBinary, Kind: "読み取れないEXE/DLL", Path: b, RelPath: windowsRel(srcFull, b), FileCount: 1})
		}
		v := info.version
		result.Binaries = append(result.Binaries, deps.Binary{File: filepath.ToSlash(rel), ProductName: v.productName, ProductVersion: v.productVersion, FileVersion: v.fileVersion, Company: v.companyName, Copyright: v.legalCopyright})
		imports[b] = info.imports
		versions[b] = v
	}

	// ---------- (2) 読み込む DLL ----------
	fmt.Println("[2/4] 読み込むDLLを調べています…")
	seenDLL := map[string]bool{}
	system32 := ""
	if windir := os.Getenv("WINDIR"); windir != "" {
		system32 = filepath.Join(windir, "System32")
	}
	for _, b := range bins {
		for _, name := range imports[b] {
			key := strings.ToLower(name)
			if !strings.HasSuffix(key, ".dll") || seenDLL[key] {
				continue
			}
			seenDLL[key] = true
			origin, where := originUnknown, ""
			if local, ok := localDLLs[key]; ok {
				origin, where = originBundled, local
			} else if msDLLPattern.MatchString(key) {
				origin = originMicrosoft
			} else if system32 != "" && exists(filepath.Join(system32, name)) {
				origin, where = originMicrosoft, filepath.Join(system32, name)
			}
			shown := where
			if shown == "" {
				shown = name
			}
			result.Files = append(result.Files, deps.File{Kind: "読み込むDLL", Path: shown, Origin: origin})
			switch origin {
			case originBundled:
				v := versions[where]
				result.Items = append(result.Items, deps.Item{Reason: deps.ReasonBundledDLL, Kind: "同梱DLL", Path: where, RelPath: windowsRel(srcFull, where), FileCount: 1, ProductName: v.productName, ProductVersion: v.productVersion})
			case originUnknown:
				result.Items = append(result.Items, deps.Item{Reason: deps.ReasonUnknownDLL, Kind: "読み込むDLL", Path: name, FileCount: 1})
			}
		}
	}
	// 実行時に明示ロードされるDLLはPEのimport表に現れない。成果物フォルダのDLLも候補にする。
	for name, local := range localDLLs {
		if seenDLL[name] {
			continue
		}
		v := versions[local]
		result.Items = append(result.Items, deps.Item{Reason: deps.ReasonBundledDLL, Kind: "同梱DLL", Path: local,
			RelPath: windowsRel(srcFull, local), FileCount: 1, ProductName: v.productName, ProductVersion: v.productVersion})
	}

	var loadedDLLs []string
	for key := range seenDLL {
		loadedDLLs = append(loadedDLLs, key)
	}
	sort.Strings(loadedDLLs)
	binaryEvidence := func() string {
		if e := findOtherCompilerDLL(loadedDLLs); e != "" {
			return e
		}
		return findMinGWBinary(bins)
	}

	if *buildLog != "" {
		data, err := os.ReadFile(*buildLog)
		if err != nil {
			return fail("%s を読めません: %v", *buildLog, err)
		}
		lines := decodeLog(data)
		project := indexProject(srcFull)

		// ---------- (3) ヘッダファイル ----------
		fmt.Println("[3/4] ビルドログからヘッダファイルを調べています…")
		headers := project.resolveAll(parseIncludes(lines))
		result.Toolchain = toolchainFromHeaders(headers)
		groups := map[string]*headerGroup{}
		var groupKeys []string
		for _, p := range headers {
			origin := rules.origin(p)
			result.Files = append(result.Files, deps.File{Kind: "ヘッダ", Path: p, Origin: origin})
			if origin != originExternal && origin != originProject {
				continue
			}
			dir := parentDir(p)
			key := strings.ToLower(dir) + "\x00" + origin
			if g, ok := groups[key]; ok {
				g.count++
			} else {
				groups[key] = &headerGroup{dir, origin, 1}
				groupKeys = append(groupKeys, key)
			}
		}
		for _, key := range groupKeys {
			g := groups[key]
			kind := fmt.Sprintf("ヘッダ（%d個）", g.count)
			if g.origin == originProject {
				result.Items = append(result.Items, deps.Item{Reason: deps.ReasonProjectFolder, Kind: kind, Path: g.dir, RelPath: windowsRel(srcFull, g.dir), FileCount: g.count})
			} else {
				result.Items = append(result.Items, deps.Item{Reason: deps.ReasonExternal, Kind: kind, Path: g.dir, FileCount: g.count})
			}
		}

		// ---------- (4) リンクで使ったライブラリ ----------
		fmt.Println("[4/4] ビルドログからリンクで使ったライブラリを調べています…")
		used, searched := parseLinkLibs(lines)
		for _, lib := range project.resolveAll(used) {
			origin := rules.origin(lib)
			result.Files = append(result.Files, deps.File{Kind: "リンクしたライブラリ", Path: lib, Origin: origin})
			switch origin {
			case originMicrosoft:
			case originProject:
				result.Items = append(result.Items, deps.Item{Reason: deps.ReasonProjectLibrary, Kind: "リンクしたライブラリ", Path: lib, RelPath: windowsRel(srcFull, lib), FileCount: 1})
			default:
				result.Items = append(result.Items, deps.Item{Reason: deps.ReasonExternal, Kind: "リンクしたライブラリ", Path: lib, FileCount: 1})
			}
		}
		// 成果物と一緒に配る他社のDLLが MinGW 製のこともあるので、DLL の手がかりは MSVC の記録がないときだけ使う
		evidence := findOtherCompilerInLog(lines)
		if evidence == "" && searched == 0 && len(headers) == 0 {
			evidence = binaryEvidence()
		}
		switch {
		case evidence != "":
			warnings = append(warnings, otherCompilerWarning(evidence))
		default:
			if searched == 0 {
				warnings = append(warnings, "ビルドログにリンクの記録がありません。手順のとおり _LINK_=/VERBOSE を設定してビルドし直してください")
			}
			if len(headers) == 0 {
				warnings = append(warnings, "ビルドログにインクルードの記録がありません。手順のとおり _CL_=/showIncludes を設定してビルドし直してください")
			}
		}
	} else {
		fmt.Println("[3/4] --buildlog が指定されていないため、ヘッダとリンクの調査は飛ばしました。")
		warnings = append(warnings, "--buildlog が指定されていません。静的ライブラリ（.lib）とヘッダは調べていません")
		if evidence := binaryEvidence(); evidence != "" {
			warnings = append(warnings, otherCompilerWarning(evidence))
		}
	}

	sort.SliceStable(result.Files, func(i, j int) bool {
		a, b := result.Files[i], result.Files[j]
		if a.Origin != b.Origin {
			return a.Origin < b.Origin
		}
		return strings.ToLower(a.Path) < strings.ToLower(b.Path)
	})
	return finish(*out, result, warnings)
}

// windowsRel はプロジェクト内のパスの --src からの相対パス（区切りは /）を返す。配下でなければ空。
func windowsRel(src, path string) string {
	if !hasPathPrefix(path, src) {
		return ""
	}
	if rel := relUnder(src, path); rel != "" {
		return rel
	}
	// Windows 以外で動かしたテストでも同じ結果にする
	rest := strings.ReplaceAll(path[len(strings.TrimRight(src, `\/`)):], `\`, "/")
	if rest = strings.TrimPrefix(rest, "/"); rest != "" {
		return rest
	}
	return "."
}
