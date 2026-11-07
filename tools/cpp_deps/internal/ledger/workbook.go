package ledger

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// formatRows は書式（プルダウン・色付け）を効かせる行の下限。人が行を追加しても効くよう、余裕を持たせる。
const formatRows = 2000

// textNumFmt は Excel の文字列の書式（"@"）。1.10 が 1.1 に変わるのを防ぐ。
const textNumFmt = 49

// FindingColumns はシート「調査結果」の列。
var FindingColumns = []string{"種類", "ファイル", "出どころ", "パッケージ／製品名", "バージョン", "PURL", "会社名", "著作権表示"}

// Column は列の見出しから Excel の列名（A, B, …）を返す。
func Column(col string) string {
	for i, c := range Columns {
		if c == col {
			name, _ := excelize.ColumnNumberToName(i + 1)
			return name
		}
	}
	panic("未定義の列: " + col)
}

// kindCondition は「区分がどれかに当てはまる」という Excel の式を作る。
func kindCondition(kinds []string) string {
	conds := make([]string, len(kinds))
	for i, k := range kinds {
		conds[i] = fmt.Sprintf(`$%s2="%s"`, Column(ColKind), k)
	}
	return "OR(" + strings.Join(conds, ",") + ")"
}

func blank(col string) string {
	return fmt.Sprintf("LEN(TRIM($%s2))=0", Column(col))
}

// MissingFormulas は列ごとの「必須なのに空」という条件付き書式の式を返す。Missing と同じ条件にする。
func MissingFormulas() map[string]string {
	byCol := map[string][]string{}
	for _, kind := range Kinds {
		for _, col := range required[kind] {
			byCol[col] = append(byCol[col], kind)
		}
	}
	formulas := map[string]string{}
	for col, kinds := range byCol {
		formulas[col] = fmt.Sprintf("AND(%s,%s)", blank(col), kindCondition(kinds))
	}
	var cpeKinds []string
	for _, kind := range Kinds {
		if needsCPEOrPURL[kind] {
			cpeKinds = append(cpeKinds, kind)
		}
	}
	cond := kindCondition(cpeKinds)
	vendor := Column(ColVendor)
	formulas[ColVendor] = fmt.Sprintf("AND(%s,%s,%s)", blank(ColVendor), blank(ColPURL), cond)
	formulas[ColProduct] = fmt.Sprintf(`AND(%s,%s,TRIM($%s2)<>"%s",%s)`, blank(ColProduct), blank(ColPURL), vendor, NoCPE, cond)
	last, _ := excelize.ColumnNumberToName(len(Columns))
	formulas[ColKind] = fmt.Sprintf("AND(%s,COUNTA($%s2:$%s2)>0)", blank(ColKind), Column(ColName), last)
	return formulas
}

// Write は台帳の Excel を作る。path がすでにあるかどうかは呼び出し側で確かめる。
func Write(path string, rows []Row, findings [][]string) error {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName(f.GetSheetName(0), SheetLedger); err != nil {
		return err
	}
	if _, err := f.NewSheet(SheetFindings); err != nil {
		return err
	}
	if err := writeLedgerSheet(f, rows); err != nil {
		return err
	}
	if err := writeFindingsSheet(f, findings); err != nil {
		return err
	}
	f.SetActiveSheet(0)
	return f.SaveAs(path)
}

func writeLedgerSheet(f *excelize.File, rows []Row) error {
	styles, err := newStyles(f)
	if err != nil {
		return err
	}
	last, _ := excelize.ColumnNumberToName(len(Columns))
	widths := map[string]float64{
		ColKind: 14, ColName: 24, ColVersion: 20, ColLicense: 28, ColVendor: 14, ColProduct: 16, ColPURL: 30,
		ColChecker: 12, ColNote: 28, ColLocation: 36, ColHolders: 30, ColHints: 26, ColTodo: 60,
	}
	for _, col := range Columns {
		name := Column(col)
		style := styles.text
		if ReferenceColumns[col] {
			style = styles.reference
		}
		if err := f.SetColStyle(SheetLedger, name, style); err != nil {
			return err
		}
		if err := f.SetColWidth(SheetLedger, name, name, widths[col]); err != nil {
			return err
		}
		if err := f.SetCellStr(SheetLedger, name+"1", col); err != nil {
			return err
		}
		headStyle := styles.header
		if ReferenceColumns[col] {
			headStyle = styles.referenceHeader
		}
		if err := f.SetCellStyle(SheetLedger, name+"1", name+"1", headStyle); err != nil {
			return err
		}
	}
	for i, row := range rows {
		for _, col := range Columns {
			v := row[col]
			if v == "" {
				continue
			}
			if err := f.SetCellStr(SheetLedger, fmt.Sprintf("%s%d", Column(col), i+2), v); err != nil {
				return err
			}
		}
	}
	if err := f.SetPanes(SheetLedger, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	if err := f.AutoFilter(SheetLedger, fmt.Sprintf("A1:%s%d", last, len(rows)+1), nil); err != nil {
		return err
	}
	kind := Column(ColKind)
	dv := excelize.NewDataValidation(true)
	dv.Sqref = fmt.Sprintf("%s2:%s%d", kind, kind, formatRows)
	if err := dv.SetDropList(Kinds); err != nil {
		return err
	}
	dv.SetError(excelize.DataValidationErrorStyleStop, "区分", "一覧から選んでください")
	if err := f.AddDataValidation(SheetLedger, dv); err != nil {
		return err
	}
	formulas := MissingFormulas()
	for _, col := range Columns {
		formula, ok := formulas[col]
		if !ok {
			continue
		}
		name := Column(col)
		format := styles.missing
		err := f.SetConditionalFormat(SheetLedger, fmt.Sprintf("%s2:%s%d", name, name, formatRows), []excelize.ConditionalFormatOptions{
			{Type: "formula", Criteria: formula, Format: &format},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFindingsSheet(f *excelize.File, findings [][]string) error {
	styles, err := newStyles(f)
	if err != nil {
		return err
	}
	lastName, _ := excelize.ColumnNumberToName(len(FindingColumns))
	widths := []float64{20, 60, 16, 28, 22, 40, 20, 30}
	for i, title := range FindingColumns {
		name, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColStyle(SheetFindings, name, styles.text); err != nil {
			return err
		}
		if err := f.SetColWidth(SheetFindings, name, name, widths[i]); err != nil {
			return err
		}
		if err := f.SetCellStr(SheetFindings, name+"1", title); err != nil {
			return err
		}
		if err := f.SetCellStyle(SheetFindings, name+"1", name+"1", styles.header); err != nil {
			return err
		}
	}
	for r, row := range findings {
		for c, v := range row {
			if v == "" {
				continue
			}
			name, _ := excelize.ColumnNumberToName(c + 1)
			if err := f.SetCellStr(SheetFindings, fmt.Sprintf("%s%d", name, r+2), v); err != nil {
				return err
			}
		}
	}
	if err := f.SetPanes(SheetFindings, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	return f.AutoFilter(SheetFindings, fmt.Sprintf("A1:%s%d", lastName, len(findings)+1), nil)
}

type sheetStyles struct {
	text, reference, header, referenceHeader, missing int
}

func newStyles(f *excelize.File) (sheetStyles, error) {
	var s sheetStyles
	var err error
	font := func(bold bool) *excelize.Font { return &excelize.Font{Bold: bold, Family: "Yu Gothic", Size: 11} }
	fill := func(color string) excelize.Fill {
		return excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{color}}
	}
	if s.text, err = f.NewStyle(&excelize.Style{NumFmt: textNumFmt, Font: font(false)}); err != nil {
		return s, err
	}
	if s.reference, err = f.NewStyle(&excelize.Style{NumFmt: textNumFmt, Font: font(false), Fill: fill("#EDEDED")}); err != nil {
		return s, err
	}
	if s.header, err = f.NewStyle(&excelize.Style{NumFmt: textNumFmt, Font: font(true), Fill: fill("#BDD7EE")}); err != nil {
		return s, err
	}
	if s.referenceHeader, err = f.NewStyle(&excelize.Style{NumFmt: textNumFmt, Font: font(true), Fill: fill("#D0D0D0")}); err != nil {
		return s, err
	}
	s.missing, err = f.NewConditionalStyle(&excelize.Style{Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFE699"}}})
	return s, err
}

// ReadRows は台帳のシートを見出し名で読む。返す行の番号は Excel の行番号（2 から）。
type NumberedRow struct {
	Number int
	Row    Row
}

// Read は台帳を読み、何も書かれていない行を除いて返す。
func Read(path string) ([]NumberedRow, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if idx, err := f.GetSheetIndex(SheetLedger); err != nil || idx < 0 {
		return nil, fmt.Errorf("シート「%s」がありません", SheetLedger)
	}
	all, err := f.GetRows(SheetLedger)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("シート「%s」に見出しがありません", SheetLedger)
	}
	head := map[int]string{}
	found := map[string]bool{}
	for i, title := range all[0] {
		title = strings.TrimSpace(title)
		head[i] = title
		found[title] = true
	}
	for _, col := range Columns {
		if !ReferenceColumns[col] && !found[col] {
			return nil, fmt.Errorf("シート「%s」に列「%s」がありません", SheetLedger, col)
		}
	}
	var rows []NumberedRow
	for i, cells := range all[1:] {
		row := Row{}
		for c, v := range cells {
			if title := head[c]; title != "" && v != "" {
				row[title] = v
			}
		}
		if !row.Empty() {
			rows = append(rows, NumberedRow{Number: i + 2, Row: row})
		}
	}
	return rows, nil
}
