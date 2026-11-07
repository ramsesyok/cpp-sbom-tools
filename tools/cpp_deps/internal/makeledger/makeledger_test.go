package makeledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cpp_deps/internal/deps"
	"cpp_deps/internal/ledger"
)

type fakeFile struct {
	Path    string
	License string
	Holders []string
}

func writeScan(t *testing.T, path string, files []fakeFile) {
	t.Helper()
	type holder struct {
		Holder string `json:"holder"`
	}
	type item struct {
		Path    string   `json:"path"`
		Type    string   `json:"type"`
		License *string  `json:"detected_license_expression_spdx"`
		Holders []holder `json:"holders"`
	}
	data := struct {
		Files []item `json:"files"`
	}{}
	data.Files = append(data.Files, item{Path: "proj", Type: "directory"})
	for _, f := range files {
		it := item{Path: "proj/" + f.Path, Type: "file"}
		if f.License != "" {
			lic := f.License
			it.License = &lic
		}
		for _, h := range f.Holders {
			it.Holders = append(it.Holders, holder{h})
		}
		data.Files = append(data.Files, it)
	}
	b, _ := json.Marshal(data)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildFromFake(t *testing.T, r *deps.Result, files []fakeFile, src string) []ledger.Row {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "scan.json")
	writeScan(t, p, files)
	fold := foldFor(r.Platform)
	scan, err := readScan(p, fold)
	if err != nil {
		t.Fatal(err)
	}
	return buildRows(r, scan, src, map[string]string{"libx": "MIT"})
}

func folderItem(rel string) deps.Item {
	return deps.Item{Reason: deps.ReasonProjectFolder, Kind: "ヘッダ（1個）", Path: "/p/" + rel, RelPath: rel, FileCount: 1}
}

func find(t *testing.T, rows []ledger.Row, location string) ledger.Row {
	t.Helper()
	for _, r := range rows {
		if strings.HasPrefix(r[ledger.ColLocation], location) {
			return r
		}
	}
	t.Fatalf("場所 %q の行がありません: %+v", location, rows)
	return nil
}

func TestFolderRollup(t *testing.T) {
	r := &deps.Result{Platform: deps.PlatformLinux, Src: "/p/proj", BinRel: "build", Items: []deps.Item{
		folderItem("src"), folderItem("src/own"), folderItem("third_party/foo"), folderItem("third_party/foo/detail"),
		folderItem("third_party/bar"), folderItem("third_party/bar/sub"),
	}}
	mit := func(p string) fakeFile { return fakeFile{Path: p, License: "MIT", Holders: []string{"Foo Inc"}} }
	rows := buildFromFake(t, r, []fakeFile{
		{Path: "src/a.h"}, {Path: "src/own/b.h"}, // 何も検出されない → まとめない
		mit("third_party/foo/foo.h"), mit("third_party/foo/detail/d.h"), // 完全に一致 → まとめる
		mit("third_party/bar/bar.h"), {Path: "third_party/bar/sub/s.h", License: "MIT", Holders: []string{"Other"}}, // 著作権者が違う → まとめない
		mit("vendor/x/x.c"),  // ヘッダが使われていないがライセンスを検出 → 行を作る
		mit("build/gen/g.c"), // 成果物の配下 → 行を作らない
		mit(".git/hook.c"),   // .git の配下 → 行を作らない
	}, t.TempDir())

	if len(rows) != 6 {
		for _, x := range rows {
			t.Logf("%v", x[ledger.ColLocation])
		}
		t.Fatalf("行数 = %d", len(rows))
	}
	foo := find(t, rows, "third_party/foo")
	if !strings.Contains(foo[ledger.ColLocation], "配下の 1 フォルダ") || foo[ledger.ColLicense] != "MIT" || foo[ledger.ColName] != "foo" {
		t.Errorf("foo: %+v", foo)
	}
	find(t, rows, "src/own")
	find(t, rows, "third_party/bar/sub")
	if x := find(t, rows, "vendor/x"); x[ledger.ColLicense] != "MIT" || x[ledger.ColKind] != "" {
		t.Errorf("vendor/x: %+v", x)
	}
}

func TestRowsAndOrder(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "third_party", "nl"), 0o755)
	os.WriteFile(filepath.Join(src, "third_party", "nl", "n.hpp"), []byte(
		"#define NL_VERSION_MAJOR 3\r\n#define NL_VERSION_MINOR 11\r\n#define NL_VERSION_PATCH 3\r\n#define OTHER_VERSION \"v2.1-rc\"\n"), 0o644)
	os.WriteFile(filepath.Join(src, "third_party", "nl", "skip.c"), []byte("#define C_VERSION \"9.9\"\n"), 0o644)
	r := &deps.Result{Platform: deps.PlatformLinux, Src: src,
		Packages: []deps.Package{
			{Name: "libx:amd64", Version: "1", Usage: "ヘッダのみ", PURL: "pkg:deb/ubuntu/libx@1?distro=ubuntu-24.04"},
			{Name: "liby:amd64", Version: "2", Usage: "ライブラリのみ"},
		},
		Items: []deps.Item{
			folderItem("third_party/nl"),
			{Reason: deps.ReasonProjectLibrary, Kind: "静的ライブラリ", Path: "/p/vend/libfoo.a", RelPath: "vend/libfoo.a"},
			{Reason: deps.ReasonExternal, Kind: "静的ライブラリ", Path: "/opt/libz.a"},
			{Reason: deps.ReasonUnresolved, Kind: "静的/共有ライブラリ", Path: "x/liby.so"},
		}}
	rows := buildFromFake(t, r, []fakeFile{{Path: "third_party/nl/n.hpp", License: "MIT AND Apache-2.0", Holders: []string{"A", "B"}}}, src)

	var kinds []string
	for _, x := range rows {
		kinds = append(kinds, x[ledger.ColKind])
	}
	want := "|||" + ledger.KindExternal + "|" + ledger.KindOSPackage + "|" + ledger.KindOSPackage
	if strings.Join(kinds, "|") != want {
		t.Errorf("区分の並び: %q", kinds)
	}
	nl := find(t, rows, "third_party/nl")
	if nl[ledger.ColVersion] != "2.1" && nl[ledger.ColVersion] != "3.11.3" {
		t.Errorf("バージョン: %+v", nl)
	}
	if !strings.Contains(nl[ledger.ColHints], "3.11.3（NL_VERSION_MAJOR/MINOR）") || strings.Contains(nl[ledger.ColHints], "9.9") {
		t.Errorf("バージョン候補: %q", nl[ledger.ColHints])
	}
	if nl[ledger.ColLicense] != "MIT AND Apache-2.0" || nl[ledger.ColHolders] != "A / B" {
		t.Errorf("nl: %+v", nl)
	}
	if x := find(t, rows, "x/liby.so"); !strings.Contains(x[ledger.ColTodo], "--linkdir") {
		t.Errorf("unresolved: %+v", x)
	}
	var libx, liby ledger.Row
	for _, x := range rows {
		switch x[ledger.ColName] {
		case "libx":
			libx = x
		case "liby":
			liby = x
		}
	}
	if libx[ledger.ColLicense] != "MIT" || !strings.Contains(libx[ledger.ColNote], "ヘッダのみ") || libx[ledger.ColTodo] != "" {
		t.Errorf("libx: %+v", libx)
	}
	if liby[ledger.ColLicense] != "" || liby[ledger.ColTodo] != todoLicense {
		t.Errorf("liby: %+v", liby)
	}
}

func TestWindowsRows(t *testing.T) {
	r := &deps.Result{Platform: deps.PlatformWindows, Src: `C:\Proj`, BinRel: "out",
		Toolchain: []deps.Tool{{Name: "MSVC", Version: "14.50"}},
		Items: []deps.Item{
			{Reason: deps.ReasonProjectFolder, Path: `C:\Proj\Inc`, RelPath: "Inc"},
			{Reason: deps.ReasonBundledDLL, Path: `C:\Proj\out\z.dll`, RelPath: "out/z.dll", ProductName: "zlib", ProductVersion: "1.3.1"},
			{Reason: deps.ReasonUnknownDLL, Path: "q.dll"},
		}}
	rows := buildFromFake(t, r, []fakeFile{{Path: "inc/a.h", License: "MIT", Holders: []string{"A"}}}, t.TempDir())
	// Windows では大文字小文字を区別せずに対応付ける
	if x := find(t, rows, "Inc"); x[ledger.ColLicense] != "MIT" {
		t.Errorf("大文字小文字: %+v", x)
	}
	if x := find(t, rows, "out/z.dll"); x[ledger.ColName] != "zlib" || x[ledger.ColVersion] != "1.3.1" || x[ledger.ColKind] != "" {
		t.Errorf("同梱DLL: %+v", x)
	}
	if x := find(t, rows, "q.dll"); x[ledger.ColKind] != ledger.KindExternal {
		t.Errorf("不明なDLL: %+v", x)
	}
	var msvc, sdk ledger.Row
	for _, x := range rows {
		if x[ledger.ColKind] == ledger.KindMicrosoft {
			if x[ledger.ColName] == "MSVC" {
				msvc = x
			} else {
				sdk = x
			}
		}
	}
	if msvc[ledger.ColVersion] != "14.50" || msvc[ledger.ColTodo] != "" {
		t.Errorf("MSVC: %+v", msvc)
	}
	if sdk[ledger.ColName] != "Windows SDK" || sdk[ledger.ColVersion] != "" || sdk[ledger.ColTodo] == "" {
		t.Errorf("SDK: %+v", sdk)
	}
}

func TestRootFolderName(t *testing.T) {
	r := &deps.Result{Platform: deps.PlatformLinux, Src: "/home/u/myapp", Items: []deps.Item{folderItem(".")}}
	rows := buildFromFake(t, r, []fakeFile{{Path: "a.h"}}, t.TempDir())
	if len(rows) != 1 || rows[0][ledger.ColName] != "myapp" || !strings.Contains(rows[0][ledger.ColLocation], "直下") {
		t.Errorf("rows: %+v", rows)
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	depsPath := filepath.Join(dir, "deps.json")
	if err := deps.Write(depsPath, &deps.Result{Platform: deps.PlatformLinux, Src: dir, Items: []deps.Item{folderItem("src")},
		Warnings: []string{"テスト"}, Packages: []deps.Package{{Name: "libx:amd64", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	scan := filepath.Join(dir, "scan.json")
	writeScan(t, scan, []fakeFile{{Path: "src/a.h"}})
	osenv := filepath.Join(dir, "env.json")
	os.WriteFile(osenv, []byte(`{"components":[{"name":"libx","licenses":[{"license":{"id":"MIT"}},{"license":{"name":"Foo"}},{"expression":"MIT"}]}]}`), 0o644)
	out := filepath.Join(dir, "台帳.xlsx")
	args := []string{"--deps", depsPath, "--scan", scan, "--src", dir, "--osenv", osenv, "--out", out}

	if rc := Run(args); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	rows, err := ledger.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1].Row[ledger.ColLicense] != "MIT AND Foo" {
		t.Errorf("rows: %+v", rows)
	}
	if rc := Run(args); rc != 1 {
		t.Errorf("上書きできてしまいます: rc = %d", rc)
	}
	if rc := Run([]string{"--deps", depsPath, "--scan", scan, "--src", dir, "--out", filepath.Join(dir, "b.xlsx")}); rc != 2 {
		t.Errorf("--osenv なしの rc = %d", rc)
	}
	other := filepath.Join(dir, "other.json")
	writeScan(t, other, []fakeFile{{Path: "else/a.h"}})
	if rc := Run([]string{"--deps", depsPath, "--scan", other, "--src", dir, "--osenv", osenv, "--out", filepath.Join(dir, "c.xlsx")}); rc != 1 {
		t.Errorf("違うフォルダを調べた結果の rc = %d", rc)
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"schema_version":99,"platform":"linux"}`), 0o644)
	if rc := Run([]string{"--deps", bad, "--scan", scan, "--src", dir, "--osenv", osenv, "--out", filepath.Join(dir, "d.xlsx")}); rc != 1 {
		t.Errorf("知らない版の rc = %d", rc)
	}
}
