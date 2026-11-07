package makeledger

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"cpp_deps/internal/ledger"
)

// TestLinuxResultOnAnyOS は、Linux のビルドPCで作った deps.json と build-env.cdx.json、
// Windows の作業PCの ScanCode で調べた scancode.json から、どの OS でも同じ台帳ができることを確かめる。
// testdata/linux-to-windows は git に含めず、ない場合は飛ばす。作り方は docs/ledger_spec.md の受け入れテストを参照。
func TestLinuxResultOnAnyOS(t *testing.T) {
	repo := filepath.Join("..", "..", "..", "..")
	data := filepath.Join(repo, "testdata", "linux-to-windows")
	if _, err := os.Stat(data); err != nil {
		t.Skip("検証データ（testdata/linux-to-windows）がないので飛ばします。git には含めていません")
	}
	work := t.TempDir()

	envPath := filepath.Join(work, "build-env.cdx.json")
	in, err := os.Open(filepath.Join(data, "build-env.cdx.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, gz); err != nil {
		t.Fatal(err)
	}
	out.Close()

	xlsx := filepath.Join(work, "部品台帳.xlsx")
	code := Run([]string{
		"--product", "software",
		"--delivery", work,
		"--deps", filepath.Join(data, "deps.json"),
		"--scan", filepath.Join(data, "scancode.json"),
		"--osenv", envPath,
		"--src", filepath.Join(repo, "samples", "mqtt-client-cmake"),
		"--out", xlsx,
	})
	if code != 0 {
		t.Fatalf("終了コード %d", code)
	}
	rows, err := ledger.Read(xlsx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("部品行がありません")
	}
	found := map[string]ledger.Row{}
	for _, nr := range rows {
		found[nr.Row.Get(ledger.ColLocation)] = nr.Row
		if nr.Row.Get(ledger.ColKind) == ledger.KindOSPackage {
			t.Errorf("ビルドPCのOSパッケージが製品行に入りました: %s", nr.Row.Get(ledger.ColName))
		}
	}
	if _, ok := found["src"]; !ok {
		t.Error("src の行がない")
	}
	n, ok := found["third_party/nlohmann"]
	if !ok {
		t.Fatal("third_party/nlohmann の行がない")
	}
	if got := n.Get(ledger.ColLicense); got != "MIT AND Apache-2.0" {
		t.Errorf("ライセンス %q", got)
	}
	if got := n.Get(ledger.ColVersion); got != "3.11.3" {
		t.Errorf("バージョン %q", got)
	}
}
