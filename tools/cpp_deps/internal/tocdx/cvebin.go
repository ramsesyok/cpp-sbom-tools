package tocdx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"cpp_deps/internal/ledger"
)

// cvebinRootPrefix は cve-bin-tool が SBOM に入れる、部品ではない項目の名前の始まり。
// 製品の SBOM などを取り違えて指定したことの判定にも使う。
const cvebinRootPrefix = "CVEBINTOOL"

// cvebinPart は cve-bin-tool が成果物の中に見つけた部品。
type cvebinPart struct {
	Name, Version, CPEProduct string
}

func (p cvebinPart) label() string {
	if p.Version == "" {
		return p.Name
	}
	return p.Name + " " + p.Version
}

// readCvebin は cve-bin-tool の --sbom-output（CycloneDX の JSON）から部品を読む。
func readCvebin(path string) ([]cvebinPart, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bom struct {
		BOMFormat  string `json:"bomFormat"`
		Components []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			CPE     string `json:"cpe"`
		} `json:"components"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &bom); err != nil {
		return nil, err
	}
	var parts []cvebinPart
	seen := map[string]bool{}
	fromCvebin := false
	for _, c := range bom.Components {
		if strings.HasPrefix(c.Name, cvebinRootPrefix) {
			fromCvebin = true
			continue
		}
		key := c.Name + "\x00" + c.Version
		if !seen[key] {
			seen[key] = true
			parts = append(parts, cvebinPart{c.Name, c.Version, cpeProduct(c.CPE)})
		}
	}
	if bom.BOMFormat != "CycloneDX" || !fromCvebin {
		return nil, errors.New("cve-bin-tool が作った結果ではありません")
	}
	return parts, nil
}

// cpeProduct は CPE（cpe:/a:ベンダー:製品:版、cpe:2.3:a:ベンダー:製品:版:…）の製品名を返す。
func cpeProduct(cpe string) string {
	f := strings.Split(cpe, ":")
	switch {
	case len(f) >= 4 && f[1] == "/a":
		return f[3]
	case len(f) >= 5 && f[1] == "2.3":
		return f[4]
	}
	return ""
}

var (
	rpmSource  = regexp.MustCompile(`-[^-]+-[^-]+\.src\.rpm$`)
	nameSuffix = regexp.MustCompile(`(-dev|-devel|-libs|-x64|-x86|-amd64|\.dll|\.lib|\.so|\.a|t64)$`)
)

// cvebinAliases は、製品名とライブラリの名前が別の部品。
var cvebinAliases = map[string][]string{
	"openssl": {"ssl", "crypto"},
}

// normalizeName は libssl3t64、libssl-dev、libssl-3-x64.dll を同じ ssl にする。
func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for {
		t := nameSuffix.ReplaceAllString(s, "")
		if t == s {
			t = strings.TrimRight(s, "0123456789.-_")
		}
		if t == s {
			break
		}
		s = t
	}
	return strings.TrimPrefix(s, "lib")
}

// upstreamOf は PURL の upstream=（Syft が入れるソースパッケージの名前）を返す。
func upstreamOf(purl string) string {
	_, query, ok := strings.Cut(purl, "?")
	if !ok {
		return ""
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return ""
	}
	return rpmSource.ReplaceAllString(values.Get("upstream"), "")
}

// matchesPart は、台帳の行が cve-bin-tool の部品にあたるかを返す。
// CPE製品名とソースパッケージの名前を見てから、部品名を見る。
func matchesPart(row ledger.Row, part cvebinPart) bool {
	product := strings.ToLower(strings.ReplaceAll(row.Get(ledger.ColProduct), `\`, ""))
	if product != "" && part.CPEProduct != "" && product == strings.ToLower(part.CPEProduct) {
		return true
	}
	wanted := map[string]bool{normalizeName(part.Name): true}
	if part.CPEProduct != "" {
		wanted[normalizeName(part.CPEProduct)] = true
	}
	for _, alias := range cvebinAliases[strings.ToLower(part.Name)] {
		wanted[alias] = true
	}
	for _, name := range []string{row.Get(ledger.ColName), row.Get(ledger.ColProduct), upstreamOf(row.Get(ledger.ColPURL))} {
		if n := normalizeName(name); n != "" && wanted[n] {
			return true
		}
	}
	return false
}

// sameVersion は、台帳のバージョンが検出したバージョンと同じかを返す。
// OS パッケージの 1:1.3.dfsg-3.1ubuntu2 のような版は、1.3 と同じとみなす。
func sameVersion(ledgerVersion, found string) bool {
	if found == "" {
		return true
	}
	clean := func(v string) string {
		v = strings.ToLower(strings.TrimSpace(v))
		if _, after, ok := strings.Cut(v, ":"); ok {
			v = after
		}
		return strings.TrimPrefix(v, "v")
	}
	l, f := clean(ledgerVersion), clean(found)
	if l == f {
		return true
	}
	return strings.HasPrefix(l, f) && strings.ContainsRune("-.+~", rune(l[len(f)]))
}

// compareCvebin は部品を台帳と照らし合わせ、確認できた部品の説明と、人の確認が必要な部品の説明を返す。
// 対象外の行は、誤検出と判断した記録として扱う。
func compareCvebin(parts []cvebinPart, rows []ledger.NumberedRow) (found, unresolved []string) {
	for _, part := range parts {
		var same, excluded, other *ledger.NumberedRow
		for i := range rows {
			r := &rows[i]
			if !matchesPart(r.Row, part) {
				continue
			}
			switch {
			case r.Row.Get(ledger.ColKind) == ledger.KindExcluded:
				if excluded == nil {
					excluded = r
				}
			case sameVersion(r.Row.Get(ledger.ColVersion), part.Version):
				if same == nil {
					same = r
				}
			default:
				if other == nil {
					other = r
				}
			}
		}
		switch {
		case same != nil:
			found = append(found, fmt.Sprintf("%s → %d行目 %s %s", part.label(), same.Number, same.Row.Get(ledger.ColName), same.Row.Get(ledger.ColVersion)))
		case excluded != nil:
			found = append(found, fmt.Sprintf("%s → %d行目 対象外（%s）", part.label(), excluded.Number, excluded.Row.Get(ledger.ColNote)))
		case other != nil:
			unresolved = append(unresolved, fmt.Sprintf("%s: バージョンが台帳と違います（%d行目 %s %s）", part.label(), other.Number, other.Row.Get(ledger.ColName), other.Row.Get(ledger.ColVersion)))
		default:
			unresolved = append(unresolved, part.label()+": 台帳に見当たりません")
		}
	}
	return found, unresolved
}

// reportCvebin は照合の結果を表示し、人の確認が必要な部品の数を返す。
func reportCvebin(parts []cvebinPart, rows []ledger.NumberedRow) int {
	if len(parts) == 0 {
		fmt.Println("作業4では部品が見つかりませんでした。cve-bin-tool が見分けられる部品は限られるので、漏れがないという意味ではありません。")
		return 0
	}
	found, unresolved := compareCvebin(parts, rows)
	fmt.Printf("作業4で見つかった部品 %d 件を台帳と照らし合わせました。\n", len(parts))
	for _, f := range found {
		fmt.Println("  確認済み: " + f)
	}
	for _, u := range unresolved {
		fmt.Fprintln(os.Stderr, "  要確認: "+u)
	}
	if len(unresolved) > 0 {
		fmt.Fprintln(os.Stderr, "要確認の部品は、開発担当者に確認して、次のどちらかをしてください。")
		fmt.Fprintln(os.Stderr, "  製品に入っている場合: 台帳に行を追加するか、バージョンを直す")
		fmt.Fprintln(os.Stderr, "  製品に入っていない場合: 部品名をこの名前にした行を追加し、区分を「対象外」にして、確認者と備考（理由）を書く")
	}
	return len(unresolved)
}
