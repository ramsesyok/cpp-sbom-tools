// Package ledger は部品台帳の列と「必須の項目」の定義を持ちます。
// cpp_sbom の ledger（台帳の色付け）と check・convert（変換時のチェック）の両方が使うので、
// 2つが食い違わないよう、定義はここにだけ書きます。仕様は docs/ledger_spec.md を参照。
package ledger

import (
	"strings"
)

// シート名
const (
	SheetLedger   = "部品台帳"
	SheetFindings = "調査結果"
)

// 区分
const (
	KindOSPackage = "OSパッケージ"
	KindBundled   = "同梱"
	KindExternal  = "別途導入"
	KindMicrosoft = "Microsoft"
	KindExcluded  = "対象外"
)

// Kinds は区分のプルダウンの選択肢。
var Kinds = []string{KindOSPackage, KindBundled, KindExternal, KindMicrosoft, KindExcluded}

// NoCPE は「調べても CPE がない」ことを表す、CPEベンダーの値。
const NoCPE = "なし"

// 列の見出し
const (
	ColKind     = "区分"
	ColName     = "部品名"
	ColVersion  = "バージョン"
	ColLicense  = "ライセンス"
	ColVendor   = "CPEベンダー"
	ColProduct  = "CPE製品名"
	ColPURL     = "PURL"
	ColChecker  = "確認者"
	ColNote     = "備考"
	ColLocation = "場所"
	ColHolders  = "著作権者"
	ColHints    = "バージョン候補"
	ColTodo     = "やること"
)

// Columns は台帳の列の並び。
var Columns = []string{ColKind, ColName, ColVersion, ColLicense, ColVendor, ColProduct, ColPURL, ColChecker, ColNote, ColLocation, ColHolders, ColHints, ColTodo}

// ReferenceColumns は参照用の列（記入しない。SBOM にも入れない）。
var ReferenceColumns = map[string]bool{ColLocation: true, ColHolders: true, ColHints: true, ColTodo: true}

// required は区分ごとの必須の項目。CPE または PURL の条件は別に扱う。
var required = map[string][]string{
	KindOSPackage: {ColName, ColVersion, ColLicense},
	KindBundled:   {ColName, ColVersion, ColLicense, ColChecker},
	KindExternal:  {ColName, ColVersion, ColLicense, ColChecker},
	KindMicrosoft: {ColName, ColVersion},
	KindExcluded:  {ColChecker, ColNote},
}

// needsCPEOrPURL は脆弱性の照合に CPE か PURL が必要な区分。
var needsCPEOrPURL = map[string]bool{KindBundled: true, KindExternal: true}

// Row は台帳の1行。キーは列の見出し。
type Row map[string]string

// Empty は何も書かれていない行かどうかを返す。
func (r Row) Empty() bool {
	for _, v := range r {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// Get は前後の空白を除いた値を返す。
func (r Row) Get(col string) string {
	return strings.TrimSpace(r[col])
}

// Missing は行に足りない項目を、そのまま画面に出せる文にして返す。
func Missing(r Row) []string {
	kind := r.Get(ColKind)
	if kind == "" {
		return []string{"区分が未選択です"}
	}
	req, ok := required[kind]
	if !ok {
		return []string{"区分の値が正しくありません（" + kind + "）"}
	}
	var out []string
	for _, col := range req {
		if r.Get(col) == "" {
			out = append(out, col+"が未記入です")
		}
	}
	if needsCPEOrPURL[kind] && !hasCPEOrPURL(r) {
		out = append(out, "CPE（ベンダーと製品名の両方）またはPURLが未記入です。CPEがなければCPEベンダーに「"+NoCPE+"」と書いてください")
	}
	return out
}

func hasCPEOrPURL(r Row) bool {
	vendor := r.Get(ColVendor)
	return r.Get(ColPURL) != "" || vendor == NoCPE || (vendor != "" && r.Get(ColProduct) != "")
}

// JoinLicenses は検出されたライセンス式を1つにまとめる。
// AND でつながった単純な式は分解して重複を除く。OR や括弧を含む式はそのまま1つとして扱う。
func JoinLicenses(exprs []string) string {
	uniq := LicensePieces(exprs)
	switch len(uniq) {
	case 0:
		return ""
	case 1:
		return uniq[0]
	}
	parts := make([]string, len(uniq))
	for i, e := range uniq {
		if strings.Contains(e, " ") {
			parts[i] = "(" + e + ")"
		} else {
			parts[i] = e
		}
	}
	return strings.Join(parts, " AND ")
}

// LicensePieces は JoinLicenses の分解の結果（重複なし、現れた順）を返す。
func LicensePieces(exprs []string) []string {
	var uniq []string
	seen := map[string]bool{}
	for _, e := range exprs {
		if e == "" {
			continue
		}
		pieces := []string{e}
		if !strings.Contains(e, " OR ") && !strings.Contains(e, "(") {
			pieces = strings.Split(e, " AND ")
		}
		for _, p := range pieces {
			p = strings.TrimSpace(p)
			if p != "" && !seen[p] {
				seen[p] = true
				uniq = append(uniq, p)
			}
		}
	}
	return uniq
}
