package collect

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestShellSplit(t *testing.T) {
	cases := map[string][]string{
		`g++ -c a.cpp -o a.o`:      {"g++", "-c", "a.cpp", "-o", "a.o"},
		`g++ -DX="a b" 'c d' e\ f`: {"g++", "-DX=a b", "c d", "e f"},
		`g++ -DV=\"1.0\" ""`:       {"g++", `-DV="1.0"`, ""},
		`cc "a\"b" "a\b" 'a\b'`:    {"cc", `a"b`, `a\b`, `a\b`},
		"  cc\t-c\n":               {"cc", "-c"},
	}
	for in, want := range cases {
		got, err := shellSplit(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("shellSplit(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := shellSplit(`cc "abc`); err == nil {
		t.Error("閉じていない引用符がエラーになりません")
	}
}

func TestDependencyArgs(t *testing.T) {
	got := dependencyArgs([]string{"g++", "-MD", "-MT", "x", "-MF", "y", "-MFz", "-O2", "-c", "a.cpp", "-o", "a.o", "-oa.o", "-MMD", "-MP", "-Iinc"})
	want := []string{"g++", "-O2", "a.cpp", "-Iinc", "-M"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseLinkLine(t *testing.T) {
	cases := map[string][]string{
		"/usr/lib/libfoo.a":                  {"/usr/lib/libfoo.a"},
		"  vend/lib/libfoo.so.1.2  ":         {"vend/lib/libfoo.so.1.2"},
		"libx.so":                            {"libx.so"},
		"/usr/lib/crti.o":                    nil,
		"g++ a.o -o libx.a":                  nil,
		"-lfoo (/usr/lib/libfoo.a)":          {"/usr/lib/libfoo.a"},
		"(/usr/lib/a.so.6) x (/usr/lib/b.a)": {"/usr/lib/a.so.6", "/usr/lib/b.a"},
		"(rel/libfoo.a)":                     nil,
		"全角\u3000libfoo.a":                   nil,
		"/usr/lib/libfoo.so.abc":             nil,
	}
	for in, want := range cases {
		if got := parseLinkLine(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseLinkLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLdd(t *testing.T) {
	out := "\tlinux-vdso.so.1 (0x00007ffd)\n\tlibm.so.6 => /lib/x86_64-linux-gnu/libm.so.6 (0x0000)\n\tlibz.so => not found\n\t/lib64/ld-linux-x86-64.so.2 (0x0000)\n"
	want := []string{"/lib/x86_64-linux-gnu/libm.so.6", "/lib64/ld-linux-x86-64.so.2"}
	if got := parseLdd(out); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseDpkgOwner(t *testing.T) {
	cases := map[string]string{
		"libssl-dev:amd64: /usr/include/openssl/ssl.h\n":                     "libssl-dev:amd64",
		"diversion by foo from: /x\ndiversion by foo to: /y\nbar, baz: /x\n": "bar",
		"diversion by foo from: /x\n":                                        "",
	}
	for in, want := range cases {
		if got := parseDpkgOwner(in); got != want {
			t.Errorf("parseDpkgOwner(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOwnerCandidates(t *testing.T) {
	got := ownerCandidates("/lib/x/liba.so", "/usr/lib/x/liba.so.1")
	want := []string{"/lib/x/liba.so", "/usr/lib/x/liba.so.1", "/usr/lib/x/liba.so", "/lib/x/liba.so.1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMakePURL(t *testing.T) {
	osinfo := parseOSRelease("ID=ubuntu\nVERSION_ID=\"24.04\"\nNAME=\"Ubuntu\"\n")
	got := makePURL("dpkg", osinfo, pkgInfo{name: "libssl-dev:amd64", version: "3.0.13-0ubuntu3.5~x+1:a", arch: "amd64", source: "openssl"})
	want := "pkg:deb/ubuntu/libssl-dev@3.0.13-0ubuntu3.5~x+1%3Aa?arch=amd64&distro=ubuntu-24.04&upstream=openssl"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	got = makePURL("dpkg", osinfo, pkgInfo{name: "foo:amd64", version: "1", arch: "amd64", source: "foo"})
	if got != "pkg:deb/ubuntu/foo@1?arch=amd64&distro=ubuntu-24.04" {
		t.Errorf("上流名の省略: %s", got)
	}
	got = makePURL("rpm", map[string]string{"ID": "rocky", "VERSION_ID": "9.4"}, pkgInfo{name: "openssl-devel", version: "3.0.7-27.el9"})
	if got != "pkg:rpm/rocky/openssl-devel@3.0.7-27.el9?distro=rocky-9.4" {
		t.Errorf("rpm: %s", got)
	}
}

func TestUseForm(t *testing.T) {
	h, l := map[string]bool{"ヘッダ": true}, map[string]bool{"ライブラリ": true}
	both := map[string]bool{"ヘッダ": true, "ライブラリ": true}
	for _, c := range []struct {
		use  map[string]bool
		has  bool
		want string
	}{{h, true, "ヘッダのみ"}, {h, false, "ヘッダ"}, {l, true, "ライブラリのみ"}, {both, true, "ヘッダとライブラリ"}} {
		if got := useForm(c.use, c.has); got != c.want {
			t.Errorf("useForm(%v, %v) = %s, want %s", c.use, c.has, got, c.want)
		}
	}
}

func TestParseIncludes(t *testing.T) {
	lines := decodeLog([]byte("\xEF\xBB\xBF1>  Note: including file:  C:\\a\\x.h\r\n  note: Including File: C:\\A\\X.H\r\nメモ: インクルード ファイル:   C:\\b\\y.h\r\nother\r\n"))
	want := []string{`C:\a\x.h`, `C:\b\y.h`}
	if got := parseIncludes(lines); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseLinkLibs(t *testing.T) {
	log := `1>  Searching C:\libs\used.lib:
1>    Found _foo
1>      Referenced in main.obj
1>  Searching C:\libs\unused.lib:
1>  Searching C:\libs\USED.lib:
1>    Found _bar
2>  Searching D:\two.lib:
1>  Finished searching libraries
1>    Found _ignored
2>    Found _baz
Searching C:\plain.lib:
  Found _p`
	used, searched := parseLinkLibs(strings.Split(log, "\n"))
	want := []string{`C:\libs\used.lib`, `D:\two.lib`, `C:\plain.lib`}
	if !reflect.DeepEqual(used, want) || searched != 5 {
		t.Errorf("got %q (%d), want %q", used, searched, want)
	}
}

// 実機の MSVC（日本語の言語パックのみ）で出た形式
func TestParseLinkLibsJapanese(t *testing.T) {
	log := `ライブラリを検索しています。
    C:\Program Files\VC\lib\x64\delayimp.lib を検索中:
    C:\work\vend\lib\vfoo.lib を検索中:
      vfoo が見つかりました。
        main.obj で参照されています。
        vfoo.lib(foo.obj) を読み込みました。
    proj\out\dpart.lib を検索中:
      "void __cdecl f(void)" (?f@@YAXXZ) が見つかりました。
1>    D:\m\n.lib を検索中:
1>      n が見つかりました。
ライブラリの検索が終了しました。
      after が見つかりました。`
	used, searched := parseLinkLibs(strings.Split(log, "\n"))
	want := []string{`C:\work\vend\lib\vfoo.lib`, `proj\out\dpart.lib`, `D:\m\n.lib`}
	if !reflect.DeepEqual(used, want) || searched != 4 {
		t.Errorf("got %q (%d), want %q", used, searched, want)
	}
}

func TestProjectResolve(t *testing.T) {
	root := t.TempDir()
	inc := filepath.Join(root, "inc", "a.h")
	dup1, dup2 := filepath.Join(root, "x", "dup.h"), filepath.Join(root, "y", "dup.h")
	lib := filepath.Join(root, "out", "Foo.lib")
	for _, p := range []string{inc, dup1, dup2, lib, filepath.Join(root, ".git", "a.h")} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	idx := indexProject(root)
	cases := map[string]string{
		`..\inc\a.h`:     inc,
		`.\inc\A.H`:      inc,
		`inc/a.h`:        inc,
		`foo.lib`:        lib,
		`out\foo.lib`:    lib,
		`dup.h`:          `dup.h`, // 候補が2つなら置き換えない
		`other\foo.lib`:  `other\foo.lib`,
		`C:\abs\foo.lib`: `C:\abs\foo.lib`,
		`\\srv\foo.lib`:  `\\srv\foo.lib`,
	}
	for in, want := range cases {
		if got := idx.resolve(in); got != want {
			t.Errorf("resolve(%q) = %q, want %q", in, got, want)
		}
	}
	if got := idx.resolveAll([]string{`..\inc\a.h`, inc, `missing.h`}); !reflect.DeepEqual(got, []string{inc, `missing.h`}) {
		t.Errorf("resolveAll = %q", got)
	}
	if got := indexProject("").resolve(`inc\a.h`); got != `inc\a.h` {
		t.Errorf("--src なし: %q", got)
	}
}

func TestOrigin(t *testing.T) {
	r := originRules{msRoots: []string{`C:\Windows`}, src: `C:\proj`, bin: `C:\out`}
	cases := map[string]string{
		"":                                originUnknown,
		`D:\x\Windows Kits\10\a.h`:        originMicrosoft,
		`D:\VS\microsoft visual studio\b`: originMicrosoft,
		`c:\windows\system32\a.dll`:       originMicrosoft,
		`C:\WindowsApps\a.dll`:            originExternal, // C:\Windows の配下ではない
		`C:\PROJ\src\a.h`:                 originProject,
		`C:/proj/src/a.h`:                 originProject,
		`C:\proj2\a.h`:                    originExternal, // C:\proj の配下ではない
		`C:\out\a.lib`:                    originBundled,
		`D:\vendor\a.lib`:                 originExternal,
	}
	for in, want := range cases {
		if got := r.origin(in); got != want {
			t.Errorf("origin(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParentDir(t *testing.T) {
	for in, want := range map[string]string{`C:\a\b\x.h`: `C:\a\b`, `C:\x.h`: `C:\`, `a/b.h`: "a", `x.h`: "", `C:\a\..\b\x.h`: `C:\a\..\b`} {
		if got := parentDir(in); got != want {
			t.Errorf("parentDir(%q) = %q, want %q", in, got, want)
		}
	}
}

// versionBlockBytes は VS_VERSIONINFO のブロックを組み立てる。
func versionBlockBytes(key string, wType uint16, value []byte, children ...[]byte) []byte {
	b := make([]byte, 6)
	b = append(b, wstr(key)...)
	pad := func() {
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
	}
	pad()
	b = append(b, value...)
	for _, c := range children {
		pad()
		b = append(b, c...)
	}
	binary.LittleEndian.PutUint16(b[0:], uint16(len(b)))
	binary.LittleEndian.PutUint16(b[4:], wType)
	return b
}

func wstr(s string) []byte {
	var b []byte
	for _, c := range utf16.Encode([]rune(s + "\x00")) {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return b
}

func TestParseVersionResource(t *testing.T) {
	str := func(k, v string) []byte { return versionBlockBytes(k, 1, wstr(v)) }
	en := versionBlockBytes("040904B0", 1, nil, str("ProductName", "English"))
	ja := versionBlockBytes("041104B0", 1, nil,
		str("CompanyName", "会社"), str("ProductName", "製品"), str("ProductVersion", "1.2.3"),
		str("FileVersion", "1.2.3.4"), str("LegalCopyright", "(c) 2026"))
	data := versionBlockBytes("VS_VERSION_INFO", 0, make([]byte, 52),
		versionBlockBytes("StringFileInfo", 1, nil, en, ja),
		versionBlockBytes("VarFileInfo", 1, nil, versionBlockBytes("Translation", 0, []byte{0x11, 0x04, 0xB0, 0x04})))
	got := parseVersionResource(data)
	want := versionInfo{productName: "製品", productVersion: "1.2.3", fileVersion: "1.2.3.4", companyName: "会社", legalCopyright: "(c) 2026"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// 壊れたデータでも落ちないこと
	for i := range data {
		parseVersionResource(data[:i])
	}
}

func TestFindOtherCompiler(t *testing.T) {
	hits := map[string]string{
		"gcc -c main.c -o main.o":                       "gcc",
		"C:/msys64/mingw64/bin/c++.exe -O2 -c main.cpp": "c++.exe",
		"  x86_64-w64-mingw32-g++.exe -c main.cpp":      "x86_64-w64-mingw32-g++.exe",
		`"C:\llvm\bin\clang++.exe" -c main.cpp`:         "clang++.exe",
		"1>  clang-cl /c main.cpp":                      "clang-cl",
		"clang-18 -c main.c":                            "clang-18",
	}
	for line, want := range hits {
		if got := findOtherCompilerInLog([]string{"make: Entering directory", line}); got != want {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
	misses := []string{
		"CL.exe /c /showIncludes main.cpp",
		`link.exe /OUT:app.exe gcc.lib`,
		`メモ: インクルード ファイル:  C:\include\gcc\foo.h`,
		"building the gcc plugin",
	}
	for _, line := range misses {
		if got := findOtherCompilerInLog([]string{line}); got != "" {
			t.Errorf("%q: got %q, want none", line, got)
		}
	}
	if got := findOtherCompilerDLL([]string{"kernel32.dll", "libstdc++-6.dll"}); got != "libstdc++-6.dll" {
		t.Errorf("DLL: got %q", got)
	}
	if got := findOtherCompilerDLL([]string{"kernel32.dll", "vcruntime140.dll", "libcurl.dll"}); got != "" {
		t.Errorf("DLL: got %q", got)
	}
}
