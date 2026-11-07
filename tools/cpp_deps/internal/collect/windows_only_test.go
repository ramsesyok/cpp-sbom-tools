//go:build windows

package collect

import (
	"debug/pe"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"cpp_deps/internal/deps"
)

func TestDecodeCP932(t *testing.T) {
	// 「メモ: インクルード ファイル:  C:\a.h」の Shift_JIS
	data := []byte("\x83\x81\x83\x82: \x83\x43\x83\x93\x83\x4e\x83\x8b\x81\x5b\x83\x68 \x83\x74\x83\x40\x83\x43\x83\x8b:  C:\\a.h\r\n")
	got := parseIncludes(decodeLog(data))
	if len(got) != 1 || got[0] != `C:\a.h` {
		t.Errorf("got %q", got)
	}
}

// debug/pe の結果と突き合わせる（序数だけで読み込むDLLと遅延読み込みは ImportedSymbols に出ないので、部分集合で比べる）。
func TestImportsAgainstDebugPE(t *testing.T) {
	sys := filepath.Join(os.Getenv("WINDIR"), "System32")
	for _, name := range []string{"notepad.exe", "cmd.exe", "kernel32.dll", "user32.dll"} {
		p := filepath.Join(sys, name)
		f, err := pe.Open(p)
		if err != nil {
			t.Skip(err)
		}
		syms, _ := f.ImportedSymbols()
		f.Close()
		info, err := readPE(p)
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]bool{}
		for _, d := range info.imports {
			have[strings.ToLower(d)] = true
		}
		missing := map[string]bool{}
		for _, s := range syms {
			if i := strings.LastIndexByte(s, ':'); i >= 0 && !have[strings.ToLower(s[i+1:])] {
				missing[s[i+1:]] = true
			}
		}
		if len(missing) > 0 {
			var m []string
			for k := range missing {
				m = append(m, k)
			}
			sort.Strings(m)
			t.Errorf("%s: 読み取れなかったDLL %v", name, m)
		}
		if len(info.imports) == 0 {
			t.Errorf("%s: 読み込むDLLが0件です", name)
		}
	}
}

func TestRunWindows(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	bin := filepath.Join(proj, "out")
	os.MkdirAll(bin, 0o755)
	src, err := os.ReadFile(filepath.Join(os.Getenv("WINDIR"), "System32", "notepad.exe"))
	if err != nil {
		t.Skip(err)
	}
	os.WriteFile(filepath.Join(bin, "app.exe"), src, 0o644)
	os.WriteFile(filepath.Join(bin, "broken.dll"), []byte("not a PE"), 0o644)
	vs := `C:\Program Files\Microsoft Visual Studio\18\Community\VC\Tools\MSVC\14.50.1\include\`
	kits := `C:\Program Files (x86)\Windows Kits\10\Include\10.0.26100.0\ucrt\`
	log := filepath.Join(root, "build.log")
	os.WriteFile(log, []byte(strings.Join([]string{
		`Note: including file:  ` + proj + `\inc\a.h`,
		`Note: including file:  ` + proj + `\inc\b.h`,
		`Note: including file:  D:\vendor\inc\v.h`,
		`Note: including file:  ` + kits + `w.h`,
		`Note: including file:  ` + vs + `x.h`,
		`Searching ` + proj + `\lib\own.lib:`,
		`  Found _own`,
		`Searching D:\vendor\lib\v.lib:`,
		`  Found _v`,
		`Searching C:\Program Files (x86)\Windows Kits\10\Lib\k.lib:`,
		`  Found _k`,
		`Finished searching libraries`,
	}, "\r\n")), 0o644)
	out := filepath.Join(root, "deps.json")
	if rc := runWindows([]string{"--bin", bin, "--src", proj, "--buildlog", log, "--out", out}); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	r, err := deps.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Platform != deps.PlatformWindows || r.BinRel != "out" {
		t.Errorf("platform=%s bin_rel=%s", r.Platform, r.BinRel)
	}
	if len(r.Toolchain) != 2 || r.Toolchain[0] != (deps.Tool{Name: "MSVC", Version: "14.50.1"}) || r.Toolchain[1] != (deps.Tool{Name: "Windows SDK", Version: "10.0.26100.0"}) {
		t.Errorf("toolchain: %+v", r.Toolchain)
	}
	if len(r.Binaries) != 2 {
		t.Errorf("binaries: %+v", r.Binaries)
	}
	var got []string
	for _, it := range r.Items {
		got = append(got, it.Reason+"|"+it.Kind+"|"+it.RelPath)
		if strings.Contains(it.Path, "Windows Kits") {
			t.Errorf("Microsoft製が items に出ています: %+v", it)
		}
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"project_folder|ヘッダ（2個）|inc",
		"project_library|リンクしたライブラリ|lib/own.lib",
		"external|ヘッダ（1個）|",
		"external|リンクしたライブラリ|",
		"unreadable_binary|読み取れないEXE/DLL|out/broken.dll",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("items に %q がありません:\n%s", want, joined)
		}
	}
}

func TestRunWindowsNoBuildLogAndMissingBin(t *testing.T) {
	if rc := runWindows(nil); rc != 2 {
		t.Errorf("引数なしの rc = %d", rc)
	}
	root := t.TempDir()
	if rc := runWindows([]string{"--bin", root}); rc != 2 {
		t.Errorf("--src なしの rc = %d", rc)
	}
	if rc := runWindows([]string{"--bin", filepath.Join(root, "none"), "--src", root}); rc != 1 {
		t.Errorf("存在しないフォルダの rc = %d", rc)
	}
	empty := filepath.Join(root, "bin")
	os.MkdirAll(empty, 0o755)
	out := filepath.Join(root, "o", "deps.json")
	if rc := runWindows([]string{"--bin", empty, "--src", root, "--out", out}); rc != 0 {
		t.Errorf("空フォルダの rc = %d", rc)
	}
	r, err := deps.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 0 || !strings.Contains(strings.Join(r.Warnings, ""), "--buildlog が指定されていません") {
		t.Errorf("items=%+v warnings=%q", r.Items, r.Warnings)
	}
}
