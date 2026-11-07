package makeledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"cpp_deps/internal/ledger"
)

var inventoryDistro = regexp.MustCompile(`[?&]distro=([^&]+)`)

// readSystemInventory は納入構成を調べた CycloneDX を台帳の行にする。
// ビルド環境の記録とは分け、納入するOSを含む入力だけを受け付ける。
func readSystemInventory(file string) ([]ledger.Row, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var bom struct {
		BOMFormat  string `json:"bomFormat"`
		Components []struct {
			Type, Name, Version, PURL string
			Supplier                  struct{ Name string } `json:"supplier"`
			Licenses                  []struct {
				Expression string                    `json:"expression"`
				License    struct{ ID, Name string } `json:"license"`
			} `json:"licenses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &bom); err != nil {
		return nil, err
	}
	if bom.BOMFormat != "CycloneDX" {
		return nil, fmt.Errorf("納入構成の調査結果は CycloneDX JSON を指定してください")
	}
	var rows []ledger.Row
	var distro string
	seenOS := false
	for _, c := range bom.Components {
		if c.Name == "" {
			continue
		}
		kind := ledger.KindInstalled
		if c.Type == "operating-system" {
			kind = ledger.KindSystemOS
			seenOS = true
		} else if strings.HasPrefix(c.PURL, "pkg:deb/") || strings.HasPrefix(c.PURL, "pkg:rpm/") {
			kind = ledger.KindOSPackage
			if m := inventoryDistro.FindStringSubmatch(c.PURL); m != nil {
				if distro != "" && distro != m[1] {
					return nil, fmt.Errorf("納入構成の調査結果に複数のOSが混在しています")
				}
				distro = m[1]
			}
		}
		var licenses []string
		for _, l := range c.Licenses {
			s := l.Expression
			if s == "" {
				s = l.License.ID
			}
			if s == "" {
				s = l.License.Name
			}
			if s != "" {
				licenses = append(licenses, s)
			}
		}
		row := ledger.Row{
			ledger.ColKind: kind, ledger.ColName: c.Name, ledger.ColVersion: c.Version,
			ledger.ColPURL: c.PURL, ledger.ColLicense: ledger.JoinLicenses(licenses),
			ledger.ColHolders: c.Supplier.Name,
			ledger.ColNote:    "納入構成の調査結果から取得",
		}
		if kind == ledger.KindInstalled && row.Get(ledger.ColPURL) == "" && row.Get(ledger.ColVendor) == "" {
			row[ledger.ColTodo] = "CPEまたはPURLを調べる。見つからなければCPEベンダーに「なし」と記入"
		}
		rows = append(rows, row)
	}
	if !seenOS {
		if distro == "" {
			return nil, fmt.Errorf("納入OSの情報がありません。完成した納入構成の調査結果を指定してください")
		}
		name, version, ok := strings.Cut(distro, "-")
		if !ok || version == "" {
			return nil, fmt.Errorf("納入OSの版を PURL から読めません: %s", distro)
		}
		rows = append([]ledger.Row{{ledger.ColKind: ledger.KindSystemOS, ledger.ColName: name, ledger.ColVersion: version,
			ledger.ColNote: "納入構成のOSパッケージから取得"}}, rows...)
	}
	return rows, nil
}
