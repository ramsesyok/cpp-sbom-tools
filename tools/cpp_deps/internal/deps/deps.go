// Package deps は cpp_deps が書き、cpp_sbom ledger が読む deps.json の形を定めます。
// 仕様は docs/cpp_deps_spec.md を参照。
package deps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// SchemaVersion は deps.json の形式の版。
const SchemaVersion = 1

const (
	PlatformLinux   = "linux"
	PlatformWindows = "windows"
)

// items の reason。判定は文言ではなくこのコードで行う。
const (
	ReasonProjectFolder    = "project_folder"
	ReasonProjectLibrary   = "project_library"
	ReasonExternal         = "external"
	ReasonUnresolved       = "unresolved"
	ReasonBundledDLL       = "bundled_dll"
	ReasonUnknownDLL       = "unknown_dll"
	ReasonUnreadableBinary = "unreadable_binary"
)

var reasonOrder = map[string]int{
	ReasonProjectFolder:    0,
	ReasonProjectLibrary:   1,
	ReasonExternal:         2,
	ReasonUnresolved:       3,
	ReasonBundledDLL:       4,
	ReasonUnknownDLL:       5,
	ReasonUnreadableBinary: 6,
}

type Result struct {
	SchemaVersion int       `json:"schema_version"`
	Platform      string    `json:"platform"`
	Distro        string    `json:"distro"`
	Src           string    `json:"src"`
	BinRel        string    `json:"bin_rel"`
	Warnings      []string  `json:"warnings"`
	Packages      []Package `json:"packages"`
	Items         []Item    `json:"items"`
	Files         []File    `json:"files"`
	Binaries      []Binary  `json:"binaries"`
	Toolchain     []Tool    `json:"toolchain"`
}

type Package struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Source    string `json:"source"`
	Usage     string `json:"usage"`
	FileCount int    `json:"file_count"`
	PURL      string `json:"purl"`
}

type Item struct {
	Reason         string `json:"reason"`
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	RelPath        string `json:"rel_path"`
	FileCount      int    `json:"file_count"`
	ProductName    string `json:"product_name,omitempty"`
	ProductVersion string `json:"product_version,omitempty"`
}

type File struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Origin  string `json:"origin"`
	Package string `json:"package,omitempty"`
	Version string `json:"version,omitempty"`
	PURL    string `json:"purl,omitempty"`
}

type Binary struct {
	File           string `json:"file"`
	ProductName    string `json:"product_name"`
	ProductVersion string `json:"product_version"`
	FileVersion    string `json:"file_version"`
	Company        string `json:"company"`
	Copyright      string `json:"copyright"`
}

type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// SortItems は reason の順、次にパスの順に並べる。
func SortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if a, b := reasonOrder[items[i].Reason], reasonOrder[items[j].Reason]; a != b {
			return a < b
		}
		return strings.ToLower(items[i].Path) < strings.ToLower(items[j].Path)
	})
}

// Write は deps.json を UTF-8（BOM なし）で書く。項目がないものは空の配列にする。
func Write(path string, r *Result) error {
	r.SchemaVersion = SchemaVersion
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	if r.Packages == nil {
		r.Packages = []Package{}
	}
	if r.Items == nil {
		r.Items = []Item{}
	}
	if r.Files == nil {
		r.Files = []File{}
	}
	if r.Binaries == nil {
		r.Binaries = []Binary{}
	}
	if r.Toolchain == nil {
		r.Toolchain = []Tool{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// Read は deps.json を読む。知らない版なら失敗する。
func Read(path string) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Result
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")), &r); err != nil {
		return nil, fmt.Errorf("形式が正しくありません: %v", err)
	}
	if r.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("このツールが対応していない版です（schema_version %d）。cpp_deps を同じ版のツールで実行し直してください", r.SchemaVersion)
	}
	if r.Platform != PlatformLinux && r.Platform != PlatformWindows {
		return nil, fmt.Errorf("platform の値が正しくありません: %q", r.Platform)
	}
	return &r, nil
}
