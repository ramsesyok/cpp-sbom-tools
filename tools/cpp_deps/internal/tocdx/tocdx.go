// Package tocdx は cpp_sbom の check と convert の本体です。部品台帳（Excel）を CycloneDX 1.5 に変換します。
// 仕様は docs/ledger_spec.md を参照。
package tocdx

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"cpp_deps/internal/ledger"
)

const toolName = "cpp_sbom"

type license struct {
	Expression string `json:"expression"`
}

type property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type component struct {
	Type       string     `json:"type"`
	BomRef     string     `json:"bom-ref"`
	Name       string     `json:"name"`
	Version    string     `json:"version"`
	CPE        string     `json:"cpe,omitempty"`
	PURL       string     `json:"purl,omitempty"`
	Licenses   []license  `json:"licenses,omitempty"`
	Properties []property `json:"properties,omitempty"`
}

var cpeSpecial = regexp.MustCompile(`[^a-z0-9_\-.~]`)

func cpeEscape(s string) string {
	s = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "_")
	return cpeSpecial.ReplaceAllStringFunc(s, func(m string) string { return `\` + m })
}

func buildCPE(vendor, product, version string) string {
	if vendor == "" || product == "" || vendor == ledger.NoCPE {
		return ""
	}
	ver := cpeEscape(version)
	if ver == "" {
		ver = "*"
	}
	return fmt.Sprintf("cpe:2.3:a:%s:%s:%s:*:*:*:*:*:*:*", cpeEscape(vendor), cpeEscape(product), ver)
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

type merged struct {
	row      ledger.Row
	number   int
	licenses []string
}

// convert は台帳の行を部品にする。止めるべき問題があれば errs に入れる。
func convert(rows []ledger.NumberedRow, allowPending bool) (comps []component, warnings, errs []string, excluded int) {
	var order []*merged
	byKey := map[string]*merged{}
	for _, nr := range rows {
		r := nr.Row
		if missing := ledger.Missing(r); len(missing) > 0 {
			if !allowPending {
				for _, m := range missing {
					errs = append(errs, fmt.Sprintf("%d行目（%s）: %s", nr.Number, kindLabel(r), m))
				}
			} else {
				warnings = append(warnings, fmt.Sprintf("%d行目（%s）は未記入があるので飛ばしました", nr.Number, kindLabel(r)))
			}
			continue
		}
		if r.Get(ledger.ColKind) == ledger.KindExcluded {
			excluded++
			continue
		}
		key := r.Get(ledger.ColName) + "\x00" + r.Get(ledger.ColVersion)
		m := byKey[key]
		if m == nil {
			m = &merged{row: r, number: nr.Number}
			byKey[key] = m
			order = append(order, m)
		} else {
			name := r.Get(ledger.ColName)
			ver := r.Get(ledger.ColVersion)
			if a, b := cpeOf(m.row), cpeOf(r); a != b {
				errs = append(errs, fmt.Sprintf("%d行目と%d行目: %s %s の CPE が食い違っています", m.number, nr.Number, name, ver))
			}
			if a, b := r.Get(ledger.ColPURL), m.row.Get(ledger.ColPURL); a != "" && b != "" && a != b {
				warnings = append(warnings, fmt.Sprintf("%d行目: %s %s の PURL が%d行目と違います（%d行目のものを使いました）", nr.Number, name, ver, m.number, m.number))
			} else if b == "" && a != "" {
				m.row[ledger.ColPURL] = a
			}
		}
		if lic := r.Get(ledger.ColLicense); lic != "" {
			m.licenses = append(m.licenses, lic)
		}
	}
	for _, m := range order {
		r := m.row
		name, ver := r.Get(ledger.ColName), r.Get(ledger.ColVersion)
		c := component{Type: "library", BomRef: name + "@" + ver, Name: name, Version: ver, CPE: cpeOf(r)}
		c.PURL = r.Get(ledger.ColPURL)
		if c.PURL == "" {
			c.PURL = "pkg:generic/" + strings.ReplaceAll(name, " ", "%20") + "@" + ver
		}
		if lic := ledger.JoinLicenses(m.licenses); lic != "" {
			c.Licenses = []license{{Expression: lic}}
		}
		for _, col := range []string{ledger.ColKind, ledger.ColChecker, ledger.ColNote} {
			if v := r.Get(col); v != "" {
				c.Properties = append(c.Properties, property{Name: "ledger:" + col, Value: v})
			}
		}
		if c.CPE == "" && r.Get(ledger.ColPURL) == "" && r.Get(ledger.ColKind) != ledger.KindMicrosoft {
			warnings = append(warnings, fmt.Sprintf("%d行目: %s にCPEもPURLもありません。脆弱性の照合ができません", m.number, name))
		}
		comps = append(comps, c)
	}
	return comps, warnings, errs, excluded
}

func kindLabel(r ledger.Row) string {
	if k := r.Get(ledger.ColKind); k != "" {
		return k
	}
	return "区分なし"
}

func cpeOf(r ledger.Row) string {
	return buildCPE(r.Get(ledger.ColVendor), r.Get(ledger.ColProduct), r.Get(ledger.ColVersion))
}

var distroPattern = regexp.MustCompile(`[?&]distro=([^&]+)`)

// addOS は OSパッケージの PURL の distro= から、OS を部品として先頭に加える。
func addOS(comps []component) ([]component, string) {
	distros := map[string]bool{}
	for _, c := range comps {
		if !strings.HasPrefix(c.PURL, "pkg:deb/") && !strings.HasPrefix(c.PURL, "pkg:rpm/") {
			continue
		}
		if m := distroPattern.FindStringSubmatch(c.PURL); m != nil {
			distros[m[1]] = true
		}
	}
	switch len(distros) {
	case 0:
		return comps, ""
	case 1:
		for d := range distros {
			name, ver, _ := strings.Cut(d, "-")
			osComp := component{Type: "operating-system", BomRef: "os:" + d, Name: name, Version: ver}
			return append([]component{osComp}, comps...), ""
		}
	}
	names := make([]string, 0, len(distros))
	for d := range distros {
		names = append(names, d)
	}
	sort.Strings(names)
	return comps, "OSパッケージのPURLに複数のOS（" + strings.Join(names, "、") + "）が混在しています。ビルド環境ごとに台帳を分けてください"
}

// inspect は台帳の行を調べ、SBOM に入れる部品と問題点を返す。変換と check の両方が使うので、判定は必ず一致する。
// libraries は OS を除いた部品の数。
func inspect(rows []ledger.NumberedRow, allowPending bool) (comps []component, libraries int, warnings, errs []string, excluded int) {
	comps, warnings, errs, excluded = convert(rows, allowPending)
	libraries = len(comps)
	comps, osWarning := addOS(comps)
	if osWarning != "" {
		warnings = append(warnings, osWarning)
	}
	return comps, libraries, warnings, errs, excluded
}

// excelLockExists は台帳が Excel で開かれているときの一時ファイルがあるかを調べる。
func excelLockExists(ledgerPath string) bool {
	dir, base := filepath.Split(ledgerPath)
	if _, err := os.Stat(filepath.Join(dir, "~$"+base)); err == nil {
		return true
	}
	runes := []rune(base)
	if len(runes) > 2 {
		if _, err := os.Stat(filepath.Join(dir, "~$"+string(runes[2:]))); err == nil {
			return true
		}
	}
	return false
}

// Run は変換を実行し、終了コードを返す。
func Run(args []string) int {
	fset := flag.NewFlagSet(toolName+" convert", flag.ContinueOnError)
	ledgerPath := fset.String("ledger", "", "部品台帳の Excel（必須）")
	name := fset.String("name", "", "製品（自社プログラム）の名前（必須）")
	version := fset.String("version", "", "製品のバージョン（必須）")
	out := fset.String("out", "product.cdx.json", "出力ファイル名")
	allowPending := fset.Bool("allow-pending", false, "未記入のある行を飛ばして変換する（試行用）")
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fset.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "エラー: 不明な引数です: %s\n", strings.Join(fset.Args(), " "))
		return 2
	}
	if *ledgerPath == "" || *name == "" || *version == "" {
		fmt.Fprintln(os.Stderr, "エラー: --ledger、--name、--version を指定してください")
		return 2
	}
	if excelLockExists(*ledgerPath) {
		fmt.Println("注意: 台帳が Excel で開かれているようです。保存していない変更は読めません")
	}
	rows, err := ledger.Read(*ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %s を読めません: %v\n", *ledgerPath, err)
		return 1
	}
	comps, libraries, warnings, errs, excluded := inspect(rows, *allowPending)
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "エラー: 台帳に足りない項目があります。記入してから再実行してください")
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  "+e)
		}
		return 1
	}

	id, err := newUUID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "エラー: 識別子を作れません: %v\n", err)
		return 1
	}
	self := *name + "@" + *version
	refs := make([]string, len(comps))
	for i, c := range comps {
		refs[i] = c.BomRef
	}
	if comps == nil {
		comps = []component{}
	}
	doc := struct {
		BomFormat    string `json:"bomFormat"`
		SpecVersion  string `json:"specVersion"`
		SerialNumber string `json:"serialNumber"`
		Version      int    `json:"version"`
		Metadata     struct {
			Timestamp string              `json:"timestamp"`
			Tools     []map[string]string `json:"tools"`
			Component component           `json:"component"`
		} `json:"metadata"`
		Components   []component `json:"components"`
		Dependencies []struct {
			Ref       string   `json:"ref"`
			DependsOn []string `json:"dependsOn"`
		} `json:"dependencies"`
	}{BomFormat: "CycloneDX", SpecVersion: "1.5", SerialNumber: "urn:uuid:" + id, Version: 1, Components: comps}
	doc.Metadata.Timestamp = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	doc.Metadata.Tools = []map[string]string{{"name": toolName}}
	doc.Metadata.Component = component{Type: "application", BomRef: self, Name: *name, Version: *version}
	doc.Dependencies = append(doc.Dependencies, struct {
		Ref       string   `json:"ref"`
		DependsOn []string `json:"dependsOn"`
	}{self, refs})

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %s を書き込めません: %v\n", *out, err)
		return 1
	}
	for _, w := range warnings {
		fmt.Println("注意: " + w)
	}
	osNote := ""
	if libraries != len(comps) {
		osNote = "（OSの情報も追加）"
	}
	fmt.Printf("完了しました。部品 %d 件%sを %s に書き出しました。", libraries, osNote, *out)
	if excluded > 0 {
		fmt.Printf("「対象外」の %d 行は入れていません。", excluded)
	}
	fmt.Println()
	return 0
}
