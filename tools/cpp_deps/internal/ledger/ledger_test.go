package ledger

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestJoinLicenses(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"MIT"}, "MIT"},
		{[]string{"MIT AND Apache-2.0", "MIT"}, "MIT AND Apache-2.0"},
		{[]string{"MIT", "BSD-3-Clause OR MIT"}, "MIT AND (BSD-3-Clause OR MIT)"},
		{[]string{"(A AND B)", "C"}, "((A AND B)) AND C"},
	}
	for _, c := range cases {
		if got := JoinLicenses(c.in); got != c.want {
			t.Errorf("JoinLicenses(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMissing(t *testing.T) {
	full := Row{ColName: "a", ColVersion: "1", ColLicense: "MIT", ColChecker: "x", ColVendor: "v", ColProduct: "p"}
	with := func(kind string, change Row) Row {
		r := Row{ColKind: kind}
		for k, v := range full {
			r[k] = v
		}
		for k, v := range change {
			r[k] = v
		}
		return r
	}
	cases := []struct {
		name string
		row  Row
		want string // 空なら問題なし
	}{
		{"区分なし", Row{ColName: "a"}, "区分が未選択"},
		{"不正な区分", Row{ColKind: "xx"}, "区分の値が正しくありません"},
		{"同梱の完成", with(KindBundled, nil), ""},
		{"確認者なし", with(KindBundled, Row{ColChecker: " "}), "確認者が未記入"},
		{"ライセンスなし", with(KindExternal, Row{ColLicense: ""}), "ライセンスが未記入"},
		{"CPEもPURLもなし", with(KindBundled, Row{ColVendor: "", ColProduct: ""}), "CPE（ベンダーと製品名の両方）"},
		{"製品名なし", with(KindBundled, Row{ColProduct: ""}), "CPE（ベンダーと製品名の両方）"},
		{"CPEなしと明記", with(KindBundled, Row{ColVendor: NoCPE, ColProduct: ""}), ""},
		{"PURLのみ", with(KindExternal, Row{ColVendor: "", ColProduct: "", ColPURL: "pkg:x/y@1"}), ""},
		{"OSはCPE不要", with(KindOSPackage, Row{ColVendor: "", ColProduct: "", ColChecker: ""}), ""},
		{"OSのライセンスなし", with(KindOSPackage, Row{ColLicense: ""}), "ライセンスが未記入"},
		{"Microsoft", Row{ColKind: KindMicrosoft, ColName: "MSVC", ColVersion: "14"}, ""},
		{"Microsoftの版なし", Row{ColKind: KindMicrosoft, ColName: "MSVC"}, "バージョンが未記入"},
		{"対象外", Row{ColKind: KindExcluded, ColChecker: "x", ColNote: "自社"}, ""},
		{"対象外の理由なし", Row{ColKind: KindExcluded, ColChecker: "x"}, "備考が未記入"},
	}
	for _, c := range cases {
		got := strings.Join(Missing(c.row), "\n")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWorkbookRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "台帳.xlsx")
	rows := []Row{
		{ColName: "foo", ColVersion: "1.10", ColLocation: "third_party/foo", ColTodo: "やること"},
		{ColKind: KindOSPackage, ColName: "bar", ColVersion: "007", ColLicense: "MIT"},
	}
	if err := Write(p, rows, [][]string{{"ヘッダ", "/a.h", "OSパッケージ", "pkg", "1.0", "", "", ""}}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Number != 2 || got[1].Number != 3 {
		t.Fatalf("got %+v", got)
	}
	if !reflect.DeepEqual(got[0].Row, rows[0]) || !reflect.DeepEqual(got[1].Row, rows[1]) {
		t.Errorf("got %+v, want %+v", got, rows)
	}

	f, err := excelize.OpenFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if sheets := f.GetSheetList(); !reflect.DeepEqual(sheets, []string{SheetLedger, SheetFindings}) {
		t.Errorf("シート: %q", sheets)
	}
	// 数値ではなく文字列として保存されていること（1.10 が 1.1 にならない）
	if typ, _ := f.GetCellType(SheetLedger, Column(ColVersion)+"2"); typ == excelize.CellTypeNumber {
		t.Errorf("バージョンが数値になっています")
	}
	dvs, err := f.GetDataValidations(SheetLedger)
	if err != nil || len(dvs) != 1 || !strings.Contains(dvs[0].Formula1, KindExcluded) {
		t.Errorf("プルダウン: %+v, %v", dvs, err)
	}
	cf, err := f.GetConditionalFormats(SheetLedger)
	if err != nil || len(cf) == 0 {
		t.Errorf("色付け: %v, %v", cf, err)
	}
	v, _ := f.GetCellValue(SheetFindings, "D2")
	if v != "pkg" {
		t.Errorf("調査結果: %q", v)
	}
}

func TestReadErrors(t *testing.T) {
	dir := t.TempDir()
	noSheet := filepath.Join(dir, "a.xlsx")
	f := excelize.NewFile()
	f.SaveAs(noSheet)
	f.Close()
	if _, err := Read(noSheet); err == nil || !strings.Contains(err.Error(), "シート") {
		t.Errorf("シートなし: %v", err)
	}
	noCol := filepath.Join(dir, "b.xlsx")
	f = excelize.NewFile()
	f.SetSheetName(f.GetSheetName(0), SheetLedger)
	f.SetCellStr(SheetLedger, "A1", ColKind)
	f.SaveAs(noCol)
	f.Close()
	if _, err := Read(noCol); err == nil || !strings.Contains(err.Error(), "列") {
		t.Errorf("列なし: %v", err)
	}
}

func TestMissingFormulasCoverRules(t *testing.T) {
	formulas := MissingFormulas()
	for _, col := range []string{ColKind, ColName, ColVersion, ColLicense, ColVendor, ColProduct, ColChecker, ColNote} {
		if formulas[col] == "" {
			t.Errorf("%s の色付けがありません", col)
		}
	}
	if !strings.Contains(formulas[ColNote], KindExcluded) || strings.Contains(formulas[ColNote], KindBundled) {
		t.Errorf("備考の式: %s", formulas[ColNote])
	}
	if !strings.Contains(formulas[ColProduct], NoCPE) {
		t.Errorf("CPE製品名の式: %s", formulas[ColProduct])
	}
}
