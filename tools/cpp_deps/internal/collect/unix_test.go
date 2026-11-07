//go:build !windows

package collect

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cpp_deps/internal/deps"
)

func TestParseDepOutput(t *testing.T) {
	out := "main.o: src/main.cpp /usr/include/a.h \\\n  ../inc/b.h\n"
	want := []string{"/proj/src/main.cpp", "/usr/include/a.h", "/inc/b.h"}
	if got := parseDepOutput(out, "/proj"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// 外部コマンドを偽物に置き換えて、処理全体を通す。
func TestRunLinuxWithFakeTools(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	tools := filepath.Join(root, "tools")
	proj := filepath.Join(root, "proj")
	opt := filepath.Join(root, "opt")
	for _, d := range []string{tools, filepath.Join(proj, "src"), filepath.Join(proj, "vend", "lib"), filepath.Join(proj, "build"), filepath.Join(opt, "inc")} {
		os.MkdirAll(d, 0o755)
	}
	write := func(p, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	optLib := filepath.Join(opt, "libopt.a")
	optHdr1, optHdr2 := filepath.Join(opt, "inc", "o1.h"), filepath.Join(opt, "inc", "o2.h")
	write(optLib, "", 0o644)
	write(optHdr1, "", 0o644)
	write(optHdr2, "", 0o644)
	write(filepath.Join(proj, "vend", "lib", "libfoo.a"), "", 0o644)
	write(filepath.Join(proj, "src", "main.cpp"), "", 0o644)
	write(filepath.Join(proj, "src", "a.hpp"), "", 0o644)
	write(filepath.Join(proj, "build", "app"), "\x7fELF", 0o755)
	write(filepath.Join(proj, "build", "note.txt"), "text", 0o644)
	write(filepath.Join(tools, "fakecc"), "#!/bin/sh\necho \"$@\" >> \""+root+"/cc.args\"\necho 'main.o: src/main.cpp src/a.hpp \\'\necho ' /usr/include/x.h "+optHdr1+" "+optHdr2+"'\n", 0o755)
	write(filepath.Join(tools, "failcc"), "#!/bin/sh\nexit 1\n", 0o755)
	write(filepath.Join(tools, "ldd"), "#!/bin/sh\nprintf '\\tlibz.so.1 => /lib/libz.so.1 (0x0)\\n'\n", 0o755)
	write(filepath.Join(tools, "dpkg"), `#!/bin/sh
case "$2" in
  /usr/include/x.h) echo "libx-dev:amd64: $2" ;;
  /usr/lib/libz.so.1) echo "zlib1g:amd64: $2" ;;
  *) exit 1 ;;
esac
`, 0o755)
	write(filepath.Join(tools, "dpkg-query"), `#!/bin/sh
for p; do :; done
case "$p" in
  libx-dev:amd64) printf '1:1.0-1\tamd64\tlibx\t1:1.0-1' ;;
  zlib1g:amd64) printf '1.3\tamd64\tzlib\t1.3' ;;
esac
`, 0o755)
	t.Setenv("PATH", tools+":/bin:/usr/bin")

	compdb := filepath.Join(root, "compile_commands.json")
	write(compdb, `[{"directory":"`+proj+`","file":"src/main.cpp","command":"fakecc -O2 -c src/main.cpp -o main.o"},
	                {"directory":"`+proj+`","file":"src/b.cpp","arguments":["failcc","-c","src/b.cpp"]}]`, 0o644)
	log := filepath.Join(root, "build.log")
	write(log, "make[1]: x\nvend/lib/libfoo.a\n"+optLib+"\nnowhere/libgone.so.1\n/nonexistent/libabs.a\n/usr/lib/crti.o\n", 0o644)

	out := filepath.Join(root, "sub", "deps.json")
	if rc := runLinux([]string{"--compdb", compdb, "--linklog", log, "--bin", filepath.Join(proj, "build"), "--src", proj, "--out", out}); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	args, _ := os.ReadFile(filepath.Join(root, "cc.args"))
	if strings.TrimSpace(string(args)) != "-O2 src/main.cpp -M" {
		t.Errorf("コンパイラの引数: %q", args)
	}
	r, err := deps.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Platform != deps.PlatformLinux || r.BinRel != "build" || r.Src != proj {
		t.Errorf("platform=%s bin_rel=%s src=%s", r.Platform, r.BinRel, r.Src)
	}

	pkgs := map[string]deps.Package{}
	for _, p := range r.Packages {
		pkgs[p.Name] = p
	}
	if p := pkgs["libx-dev:amd64"]; p.Version != "1:1.0-1" || p.Source != "libx" || p.Usage != "ヘッダのみ" || p.FileCount != 1 ||
		!strings.Contains(p.PURL, "@1%3A1.0-1?arch=amd64&distro=") || !strings.HasSuffix(p.PURL, "&upstream=libx") {
		t.Errorf("libx-dev: %+v", p)
	}
	if p := pkgs["zlib1g:amd64"]; p.Usage != "ライブラリのみ" {
		t.Errorf("zlib1g: %+v", p)
	}

	var got []string
	for _, it := range r.Items {
		got = append(got, it.Reason+"|"+it.Kind+"|"+it.Path+"|"+it.RelPath)
	}
	want := []string{
		"project_folder|ヘッダ（1個）|" + filepath.Join(proj, "src") + "|src",
		"project_library|静的ライブラリ|" + filepath.Join(proj, "vend", "lib", "libfoo.a") + "|vend/lib/libfoo.a",
		"external|ヘッダ（2個）|" + filepath.Join(opt, "inc") + "|",
		"external|静的ライブラリ|" + optLib + "|",
		"unresolved|静的/共有ライブラリ|nowhere/libgone.so.1|",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("items:\n got %q\nwant %q", got, want)
	}

	for _, want := range []string{"1 件のコンパイル命令は再実行できませんでした", "場所を特定できないライブラリが 1 件あります"} {
		if !strings.Contains(strings.Join(r.Warnings, "\n"), want) {
			t.Errorf("warnings に %q がありません: %q", want, r.Warnings)
		}
	}
	var main bool
	for _, f := range r.Files {
		main = main || strings.Contains(f.Path, "main.cpp") || strings.Contains(f.Path, "libabs.a")
	}
	if main {
		t.Errorf("files に余分な行があります: %+v", r.Files)
	}
}

func TestRunLinuxArgs(t *testing.T) {
	if rc := runLinux(nil); rc != 2 {
		t.Errorf("引数なしの rc = %d", rc)
	}
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.log")
	os.WriteFile(empty, nil, 0o644)
	if rc := runLinux([]string{"--linklog", empty}); rc != 2 {
		t.Errorf("--src なしの rc = %d", rc)
	}
	out := filepath.Join(dir, "deps.json")
	if rc := runLinux([]string{"--linklog", empty, "--src", dir, "--out", out}); rc != 0 {
		t.Errorf("空ログの rc = %d", rc)
	}
	r, err := deps.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.Warnings, ""), "リンクの記録がありません") {
		t.Errorf("warnings: %q", r.Warnings)
	}
	// 項目がないものは null ではなく空の配列にする
	b, _ := os.ReadFile(out)
	if strings.Contains(string(b), "null") {
		t.Errorf("null があります: %s", b)
	}
}
