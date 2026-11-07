package makeledger

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"cpp_deps/internal/ledger"
)

// folderScan はフォルダの直下のファイルから集めた ScanCode の結果。
type folderScan struct {
	rel      string
	licenses []string       // 現れた順
	holders  map[string]int // 著作権者 -> 回数
	order    []string       // 著作権者の現れた順
}

func (s *folderScan) detected() bool {
	return len(s.licenses) > 0 || len(s.holders) > 0
}

// signature はライセンスと著作権者の組を比べるための文字列。両方が検出されているときだけ返す。
func (s *folderScan) signature() (string, bool) {
	if len(s.licenses) == 0 || len(s.holders) == 0 {
		return "", false
	}
	lic := ledger.LicensePieces(s.licenses)
	sort.Strings(lic)
	hold := append([]string(nil), s.order...)
	sort.Strings(hold)
	return strings.Join(lic, "\x00") + "\x01" + strings.Join(hold, "\x00"), true
}

func (s *folderScan) merge(o *folderScan) {
	s.licenses = append(s.licenses, o.licenses...)
	for _, h := range o.order {
		if _, ok := s.holders[h]; !ok {
			s.order = append(s.order, h)
		}
		s.holders[h] += o.holders[h]
	}
}

// topHolders は多い順（同数なら現れた順）に最大 n 件の著作権者を返す。
func (s *folderScan) topHolders(n int) []string {
	hold := append([]string(nil), s.order...)
	sort.SliceStable(hold, func(i, j int) bool { return s.holders[hold[i]] > s.holders[hold[j]] })
	if len(hold) > n {
		hold = hold[:n]
	}
	return hold
}

type scanFile struct {
	Path        string  `json:"path"`
	Type        string  `json:"type"`
	LicenseSPDX *string `json:"detected_license_expression_spdx"`
	License     *string `json:"detected_license_expression"`
	Holders     []struct {
		Holder string `json:"holder"`
	} `json:"holders"`
}

// readScan は ScanCode の --json-pp の結果をフォルダごとに集める。
// path の先頭要素（調べたフォルダの名前）を除いたものを --src からの相対パスとして扱う。
func readScan(file string, fold func(string) string) (map[string]*folderScan, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var data struct {
		Files []scanFile `json:"files"`
	}
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, err
	}
	folders := map[string]*folderScan{}
	for _, item := range data.Files {
		if item.Type != "file" {
			continue
		}
		p := strings.ReplaceAll(item.Path, `\`, "/")
		_, rest, ok := strings.Cut(p, "/")
		if !ok {
			continue // フォルダではなくファイルを1つだけ調べた結果
		}
		rel := path.Dir(rest)
		key := fold(rel)
		s := folders[key]
		if s == nil {
			s = &folderScan{rel: rel, holders: map[string]int{}}
			folders[key] = s
		}
		if item.LicenseSPDX != nil && *item.LicenseSPDX != "" {
			s.licenses = append(s.licenses, *item.LicenseSPDX)
		} else if item.License != nil && *item.License != "" {
			s.licenses = append(s.licenses, *item.License)
		}
		for _, h := range item.Holders {
			name := strings.TrimSpace(h.Holder)
			if name == "" {
				continue
			}
			if _, seen := s.holders[name]; !seen {
				s.order = append(s.order, name)
			}
			s.holders[name]++
		}
	}
	return folders, nil
}

var (
	defineVersionString = regexp.MustCompile(`(?m)^\s*#\s*define\s+(\w*VERSION\w*)\s+"v?(\d+(?:\.\d+){1,3})[^"]*"`)
	defineVersionNumber = regexp.MustCompile(`(?m)^\s*#\s*define\s+(\w+?)_(MAJOR|MINOR|PATCH|MICRO|REVISION)\s+(\d+)\b`)
	firstVersion        = regexp.MustCompile(`^\s*(\d+(?:\.\d+)*)`)
)

var headerSuffixes = []string{".h", ".hh", ".hpp", ".hxx", ".h.in", ".inl"}

// hintCounter はバージョン候補を、現れた順を保って数える。
type hintCounter struct {
	counts map[string]int
	order  []string
}

func (c *hintCounter) add(h string) {
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	if _, ok := c.counts[h]; !ok {
		c.order = append(c.order, h)
	}
	c.counts[h]++
}

func (c *hintCounter) top(n int) []string {
	out := append([]string(nil), c.order...)
	sort.SliceStable(out, func(i, j int) bool { return c.counts[out[i]] > c.counts[out[j]] })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// addVersionHints はフォルダの直下のヘッダからバージョンらしき定義を読み取る。
func addVersionHints(c *hintCounter, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !isHeaderName(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if len(data) > 2_000_000 {
			data = data[:2_000_000]
		}
		text := strings.ToValidUTF8(string(data), "�")
		text = strings.ReplaceAll(text, "\r\n", "\n")
		for _, m := range defineVersionString.FindAllStringSubmatch(text, -1) {
			c.add(m[2] + "（" + m[1] + "）")
		}
		parts := map[string]map[string]string{}
		var prefixes []string
		for _, m := range defineVersionNumber.FindAllStringSubmatch(text, -1) {
			if parts[m[1]] == nil {
				parts[m[1]] = map[string]string{}
				prefixes = append(prefixes, m[1])
			}
			parts[m[1]][m[2]] = m[3]
		}
		for _, prefix := range prefixes {
			p := parts[prefix]
			if p["MAJOR"] == "" || p["MINOR"] == "" {
				continue
			}
			ver := p["MAJOR"] + "." + p["MINOR"]
			for _, k := range []string{"PATCH", "MICRO", "REVISION"} {
				if p[k] != "" {
					ver += "." + p[k]
					break
				}
			}
			c.add(ver + "（" + prefix + "_MAJOR/MINOR）")
		}
	}
}

func isHeaderName(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range headerSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}
