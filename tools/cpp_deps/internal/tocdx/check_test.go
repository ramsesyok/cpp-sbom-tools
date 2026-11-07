package tocdx

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cpp_deps/internal/ledger"
)

// runCheck は RunCheck を実行し、終了コードと画面への出力を返す。
func runCheck(t *testing.T, args ...string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	rc := RunCheck(args)
	os.Stdout, os.Stderr = stdout, stderr
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return rc, string(out)
}

func writeLedger(t *testing.T, rows ...ledger.Row) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "台帳.xlsx")
	if err := ledger.Write(p, rows, nil); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckComplete(t *testing.T) {
	excl := ledger.Row{ledger.ColKind: ledger.KindExcluded, ledger.ColChecker: "A", ledger.ColNote: "自社"}
	p := writeLedger(t, bundled("foo", "1", "MIT"), excl)
	rc, out := runCheck(t, "--ledger", p)
	if rc != 0 || !strings.Contains(out, "完成しています") || !strings.Contains(out, "部品は 1 件") || !strings.Contains(out, "「対象外」は 1 行") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Errorf("check がファイルを作っています: %v", entries)
	}
}

func TestCheckIncomplete(t *testing.T) {
	noLicense := bundled("foo", "1", "")
	p := writeLedger(t, ledger.Row{ledger.ColName: "src"}, noLicense)
	rc, out := runCheck(t, "--ledger", p)
	if rc != 1 || !strings.Contains(out, "次の 2 件") || !strings.Contains(out, "2行目（区分なし）: 区分が未選択") || !strings.Contains(out, "3行目（同梱）: ライセンスが未記入") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

// 変換で止まる問題は、未記入でなくても check で止まること。
func TestCheckCPEConflict(t *testing.T) {
	other := bundled("foo", "1", "MIT")
	other[ledger.ColVendor] = "w"
	rc, out := runCheck(t, "--ledger", writeLedger(t, bundled("foo", "1", "MIT"), other))
	if rc != 1 || !strings.Contains(out, "CPE が食い違って") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

// 注意だけなら完成扱いで、注意は画面に出ること。
func TestCheckWarningsOnly(t *testing.T) {
	none := bundled("x", "1", "MIT")
	none[ledger.ColVendor], none[ledger.ColProduct] = ledger.NoCPE, ""
	rc, out := runCheck(t, "--ledger", writeLedger(t, none))
	if rc != 0 || !strings.Contains(out, "注意: 2行目: x にCPEもPURLもありません") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

func TestCheckArguments(t *testing.T) {
	if rc, _ := runCheck(t); rc != 2 {
		t.Errorf("--ledger なしの rc = %d", rc)
	}
	if rc, out := runCheck(t, "--ledger", filepath.Join(t.TempDir(), "ない.xlsx")); rc != 1 || !strings.Contains(out, "読めません") {
		t.Errorf("ファイルなし: rc=%d out=%q", rc, out)
	}
}

// 実際の Excel で保存した台帳でも、check と変換の判定が一致すること。
func TestCheckAgreesWithConversion(t *testing.T) {
	p := filepath.Join("testdata", "excel_saved.xlsx")
	if rc, out := runCheck(t, "--ledger", p); rc != 0 || !strings.Contains(out, "完成しています") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
	if rc := Run([]string{"--ledger", p, "--name", "a", "--version", "1", "--out", filepath.Join(t.TempDir(), "p.json")}); rc != 0 {
		t.Errorf("変換の rc = %d", rc)
	}
}

func writeCvebin(t *testing.T, parts ...string) string {
	t.Helper()
	comps := `{"name":"CVEBINTOOL-bin"}`
	for _, p := range parts {
		comps += "," + p
	}
	path := filepath.Join(t.TempDir(), "cvebin-parts.json")
	if err := os.WriteFile(path, []byte(`{"bomFormat":"CycloneDX","components":[`+comps+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// 実際の cve-bin-tool の出力を読めて、OS パッケージの行と PURL の upstream で結び付くこと。
func TestCheckCvebinRealOutput(t *testing.T) {
	os_ := ledger.Row{
		ledger.ColKind: ledger.KindOSPackage, ledger.ColName: "libssl3t64", ledger.ColVersion: "3.0.13-0ubuntu3.16", ledger.ColLicense: "Apache-2.0",
		ledger.ColPURL: "pkg:deb/ubuntu/libssl3t64@3.0.13-0ubuntu3.16?arch=amd64&distro=ubuntu-24.04&upstream=openssl",
	}
	sample := filepath.Join("..", "..", "..", "..", "testdata", "linux-to-windows", "cvebin-parts.json")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("検証データ（testdata/linux-to-windows）がないので飛ばします。git には含めていません")
	}
	rc, out := runCheck(t, "--ledger", writeLedger(t, os_), "--cvebin", sample)
	if rc != 0 || !strings.Contains(out, "確認済み: openssl 3.0.13 → 2行目 libssl3t64") || strings.Contains(out, "CVEBINTOOL") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

func TestCheckCvebinMissing(t *testing.T) {
	p := writeCvebin(t, `{"name":"zlib","version":"1.3","cpe":"cpe:/a:zlib:zlib:1.3"}`)
	rc, out := runCheck(t, "--ledger", writeLedger(t, bundled("foo", "1", "MIT")), "--cvebin", p)
	if rc != 1 || !strings.Contains(out, "要確認: zlib 1.3: 台帳に見当たりません") || strings.Contains(out, "完成しています") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

// 名前が違っても、記入した CPE製品名で結び付くこと。
func TestCheckCvebinByCPE(t *testing.T) {
	row := bundled("my-tls", "1.1.1w", "OpenSSL")
	row[ledger.ColVendor], row[ledger.ColProduct] = "openssl", "openssl"
	p := writeCvebin(t, `{"name":"openssl","version":"1.1.1w","cpe":"cpe:2.3:a:openssl:openssl:1.1.1w:*:*:*:*:*:*:*"}`)
	if rc, out := runCheck(t, "--ledger", writeLedger(t, row), "--cvebin", p); rc != 0 || !strings.Contains(out, "2行目 my-tls") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

func TestCheckCvebinVersionDiffers(t *testing.T) {
	p := writeCvebin(t, `{"name":"foo","version":"1.1.1"}`)
	for _, v := range []string{"1.1.1w", "1.1.2"} {
		rc, out := runCheck(t, "--ledger", writeLedger(t, bundled("foo", v, "MIT")), "--cvebin", p)
		if rc != 1 || !strings.Contains(out, "バージョンが台帳と違います（2行目 foo "+v) {
			t.Errorf("%s: rc=%d out=%q", v, rc, out)
		}
	}
}

// 誤検出と判断した部品は、対象外の行があれば確認済みになること。
func TestCheckCvebinExcluded(t *testing.T) {
	excl := ledger.Row{ledger.ColKind: ledger.KindExcluded, ledger.ColName: "zlib", ledger.ColChecker: "A", ledger.ColNote: "誤検出"}
	p := writeCvebin(t, `{"name":"zlib","version":"1.3"}`)
	if rc, out := runCheck(t, "--ledger", writeLedger(t, excl), "--cvebin", p); rc != 0 || !strings.Contains(out, "対象外（誤検出）") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

// 未記入の項目と作業4の要確認は、1回でまとめて分かること。
func TestCheckCvebinWithIncompleteLedger(t *testing.T) {
	p := writeCvebin(t, `{"name":"zlib","version":"1.3"}`)
	rc, out := runCheck(t, "--ledger", writeLedger(t, bundled("foo", "1", "")), "--cvebin", p)
	if rc != 1 || !strings.Contains(out, "ライセンスが未記入") || !strings.Contains(out, "要確認: zlib 1.3") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

func TestCheckCvebinNothingFound(t *testing.T) {
	rc, out := runCheck(t, "--ledger", writeLedger(t, bundled("foo", "1", "MIT")), "--cvebin", writeCvebin(t))
	if rc != 0 || !strings.Contains(out, "漏れがないという意味ではありません") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

// 製品の SBOM などを取り違えて指定すると、すべて一致したように見えるので止めること。
func TestCheckCvebinWrongFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "product.cdx.json")
	if err := os.WriteFile(p, []byte(`{"bomFormat":"CycloneDX","components":[{"name":"foo","version":"1"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out := runCheck(t, "--ledger", writeLedger(t, bundled("foo", "1", "MIT")), "--cvebin", p)
	if rc != 1 || !strings.Contains(out, "cve-bin-tool が作った結果ではありません") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"libssl3t64": "ssl", "libssl-dev": "ssl", "libssl-3-x64.dll": "ssl", "libpng16-16": "png", "libsqlite3-0": "sqlite",
		"libcurl4t64": "curl", "curl": "curl", "zlib1.dll": "zlib", "openssl-libs": "openssl", "libxml2": "xml", "": "",
	} {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpstreamOf(t *testing.T) {
	for in, want := range map[string]string{
		"pkg:deb/ubuntu/zlib1g@1%3A1.3?arch=amd64&upstream=zlib":                                    "zlib",
		"pkg:rpm/rocky/openssl-libs@3.0.7-27.el9?arch=x86_64&upstream=openssl-3.0.7-27.el9.src.rpm": "openssl",
		"pkg:deb/ubuntu/openssl@3.0.13?arch=amd64":                                                  "",
		"": "",
	} {
		if got := upstreamOf(in); got != want {
			t.Errorf("upstreamOf(%q) = %q, want %q", in, got, want)
		}
	}
}
