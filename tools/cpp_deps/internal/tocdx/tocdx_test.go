package tocdx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cpp_deps/internal/ledger"
	"github.com/xuri/excelize/v2"
)

func withScopeFixture(t *testing.T, source string) string {
	t.Helper()
	f, err := excelize.OpenFile(source)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.NewSheet(ledger.SheetScope); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellStr(ledger.SheetScope, "B1", ledger.ScopeSoftware); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "台帳.xlsx")
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildCPE(t *testing.T) {
	cases := []struct{ v, p, ver, want string }{
		{"nlohmann", "json", "3.11.3", "cpe:2.3:a:nlohmann:json:3.11.3:*:*:*:*:*:*:*"},
		{"Eclipse", "paho mqtt/c++", "1.3~a", `cpe:2.3:a:eclipse:paho_mqtt\/c\+\+:1.3~a:*:*:*:*:*:*:*`},
		{"", "json", "1", ""},
		{ledger.NoCPE, "json", "1", ""},
		{"a", "b", "", "cpe:2.3:a:a:b:*:*:*:*:*:*:*:*"},
	}
	for _, c := range cases {
		if got := buildCPE(c.v, c.p, c.ver); got != c.want {
			t.Errorf("buildCPE(%q,%q,%q) = %q, want %q", c.v, c.p, c.ver, got, c.want)
		}
	}
}

func numbered(rows ...ledger.Row) []ledger.NumberedRow {
	out := make([]ledger.NumberedRow, len(rows))
	for i, r := range rows {
		out[i] = ledger.NumberedRow{Number: i + 2, Row: r}
	}
	return out
}

func bundled(name, ver, lic string) ledger.Row {
	return ledger.Row{ledger.ColKind: ledger.KindBundled, ledger.ColName: name, ledger.ColVersion: ver, ledger.ColLicense: lic,
		ledger.ColVendor: "v", ledger.ColProduct: "p", ledger.ColChecker: "確認者A", ledger.ColLocation: "third_party/" + name}
}

func TestMergeSameComponent(t *testing.T) {
	comps, warnings, errs, _ := convert(numbered(bundled("foo", "1", "MIT"), bundled("foo", "1", "Apache-2.0 AND MIT")), false)
	if len(errs) != 0 || len(warnings) != 0 {
		t.Fatalf("errs=%q warnings=%q", errs, warnings)
	}
	if len(comps) != 1 || comps[0].Licenses[0].Expression != "MIT AND Apache-2.0" {
		t.Errorf("comps: %+v", comps)
	}
	for _, p := range comps[0].Properties {
		if strings.Contains(p.Value, "third_party") {
			t.Errorf("場所が SBOM に入っています: %+v", p)
		}
	}

	other := bundled("foo", "1", "MIT")
	other[ledger.ColVendor] = "w"
	if _, _, errs, _ := convert(numbered(bundled("foo", "1", "MIT"), other), false); len(errs) != 1 || !strings.Contains(errs[0], "CPE が食い違って") {
		t.Errorf("CPE の食い違い: %q", errs)
	}

	a, b := bundled("foo", "1", "MIT"), bundled("foo", "1", "MIT")
	a[ledger.ColPURL], b[ledger.ColPURL] = "pkg:x/a@1", "pkg:x/b@1"
	comps, warnings, errs, _ = convert(numbered(a, b), false)
	if len(errs) != 0 || len(warnings) != 0 || len(comps) != 2 || comps[0].PURL != "pkg:x/a@1" || comps[1].PURL != "pkg:x/b@1" || comps[0].BomRef == comps[1].BomRef {
		t.Errorf("異なるPURLの部品: %+v %q %q", comps, warnings, errs)
	}
}

func TestExcludedAndPending(t *testing.T) {
	excl := ledger.Row{ledger.ColKind: ledger.KindExcluded, ledger.ColChecker: "A", ledger.ColNote: "自社"}
	pending := ledger.Row{ledger.ColName: "src"}
	comps, _, errs, excluded := convert(numbered(excl, pending), false)
	if len(comps) != 0 || excluded != 1 || len(errs) != 1 || !strings.Contains(errs[0], "3行目（区分なし）: 区分が未選択") {
		t.Errorf("comps=%+v excluded=%d errs=%q", comps, excluded, errs)
	}
	comps, warnings, errs, _ := convert(numbered(pending, bundled("a", "1", "MIT")), true)
	if len(errs) != 0 || len(comps) != 1 || len(warnings) != 1 {
		t.Errorf("--allow-pending: %+v %q %q", comps, warnings, errs)
	}
}

func TestNoCPEWarnings(t *testing.T) {
	none := bundled("x", "1", "MIT")
	none[ledger.ColVendor], none[ledger.ColProduct] = ledger.NoCPE, ""
	ms := ledger.Row{ledger.ColKind: ledger.KindMicrosoft, ledger.ColName: "MSVC", ledger.ColVersion: "14"}
	comps, warnings, errs, _ := convert(numbered(none, ms), false)
	if len(errs) != 0 || len(comps) != 2 {
		t.Fatalf("%+v %q", comps, errs)
	}
	if comps[0].CPE != "" || comps[0].PURL != "" {
		t.Errorf("なし: %+v", comps[0])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "x にCPEもPURLもありません") {
		t.Errorf("Microsoft には注意を出さない: %q", warnings)
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "台帳.xlsx")
	os1 := ledger.Row{ledger.ColKind: ledger.KindOSPackage, ledger.ColName: "libx", ledger.ColVersion: "1.10", ledger.ColLicense: "MIT", ledger.ColPURL: "pkg:deb/ubuntu/libx@1.10?distro=ubuntu-24.04"}
	if err := ledger.Write(p, []ledger.Row{os1, bundled("foo", "2.0", "MIT")}, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "p.cdx.json")
	if rc := Run([]string{"--ledger", p, "--name", "app", "--version", "1", "--out", out}); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	var bom map[string]any
	data, _ := os.ReadFile(out)
	if err := json.Unmarshal(data, &bom); err != nil {
		t.Fatal(err)
	}
	if bom["bomFormat"] != "CycloneDX" || bom["specVersion"] != "1.5" {
		t.Errorf("bom: %v", bom)
	}
	comps := bom["components"].([]any)
	if len(comps) != 2 || comps[0].(map[string]any)["type"] == "operating-system" || comps[0].(map[string]any)["version"] != "1.10" {
		t.Errorf("components: %v", comps)
	}
	if !strings.Contains(string(data), `"ledger:区分"`) || strings.Contains(string(data), "third_party") {
		t.Errorf("properties: %s", data)
	}
	if rc := Run([]string{"--ledger", p}); rc != 2 {
		t.Errorf("引数不足の rc = %d", rc)
	}

	pendingPath := filepath.Join(dir, "未記入.xlsx")
	ledger.Write(pendingPath, []ledger.Row{{ledger.ColName: "x"}}, nil)
	if rc := Run([]string{"--ledger", pendingPath, "--name", "a", "--version", "1", "--out", filepath.Join(dir, "n.json")}); rc != 1 {
		t.Errorf("未記入の rc = %d", rc)
	}
	if _, err := os.Stat(filepath.Join(dir, "n.json")); err == nil {
		t.Errorf("未記入なのに SBOM ができています")
	}
	os.WriteFile(filepath.Join(dir, "~$台帳.xlsx"), nil, 0o644)
	if !excelLockExists(p) {
		t.Errorf("Excel の一時ファイルを見つけられません")
	}
}

func TestSystemScopeNeedsDeliveredOS(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "system.xlsx")
	if err := ledger.WriteWithScope(p, ledger.ScopeSystem, []ledger.Row{bundled("foo", "1", "MIT")}, nil); err != nil {
		t.Fatal(err)
	}
	if rc := Run([]string{"--ledger", p, "--name", "system", "--version", "1", "--out", filepath.Join(dir, "bad.json")}); rc != 1 {
		t.Fatalf("納入OSなしの終了コード = %d", rc)
	}
	if err := ledger.WriteWithScope(p, ledger.ScopeSystem, []ledger.Row{
		{ledger.ColKind: ledger.KindSystemOS, ledger.ColName: "Windows 11", ledger.ColVersion: "10.0.1"},
		bundled("foo", "1", "MIT"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "system.json")
	if rc := Run([]string{"--ledger", p, "--name", "system", "--version", "1", "--out", out}); rc != 0 {
		t.Fatalf("終了コード = %d", rc)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), `"type": "operating-system"`) {
		t.Fatalf("納入OSがありません: %s", data)
	}
}

// 実際の Excel で記入して保存した台帳（共有文字列など、Excel の保存形式）を読めること。
func TestReadExcelSavedLedger(t *testing.T) {
	out := filepath.Join(t.TempDir(), "p.cdx.json")
	if rc := Run([]string{"--ledger", withScopeFixture(t, filepath.Join("testdata", "excel_saved.xlsx")), "--name", "mqtt_sample", "--version", "1.0.0", "--out", out}); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var bom struct {
		Components []component `json:"components"`
	}
	if err := json.Unmarshal(data, &bom); err != nil {
		t.Fatal(err)
	}
	found := map[string]component{}
	for _, c := range bom.Components {
		found[c.Name] = c
	}
	if c := found["added-lib"]; c.Version != "1.10" {
		t.Errorf("バージョンが文字列のまま読めていません: %+v", c)
	}
	if c := found["nlohmann-json"]; c.CPE != "cpe:2.3:a:nlohmann:json:3.11.3:*:*:*:*:*:*:*" || c.Licenses[0].Expression != "MIT AND Apache-2.0" {
		t.Errorf("nlohmann-json: %+v", c)
	}
	if _, ok := found["src"]; ok {
		t.Errorf("対象外の行が入っています")
	}
	if len(bom.Components) != 15 || bom.Components[0].Type == "operating-system" {
		t.Errorf("部品数 = %d", len(bom.Components))
	}
}
