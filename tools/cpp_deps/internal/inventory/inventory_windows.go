//go:build windows

package inventory

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yusufpapurcu/wmi"
	"golang.org/x/sys/windows/registry"
)

type component struct {
	Type     string    `json:"type"`
	Name     string    `json:"name"`
	Version  string    `json:"version"`
	Supplier *supplier `json:"supplier,omitempty"`
}

type supplier struct {
	Name string `json:"name"`
}

type bom struct {
	BOMFormat   string      `json:"bomFormat"`
	SpecVersion string      `json:"specVersion"`
	Version     int         `json:"version"`
	Components  []component `json:"components"`
}

type operatingSystem struct{ Caption, Version string }
type hotfix struct{ HotFixID, InstalledOn, Description string }

func Run(args []string) int {
	fs := flag.NewFlagSet("cpp_deps inventory", flag.ContinueOnError)
	outDir := fs.String("outdir", "C:\\sbom-work", "結果を保存するフォルダ")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "エラー: 不明な引数です")
		return 2
	}
	if err := collect(*outDir); err != nil {
		fmt.Fprintln(os.Stderr, "エラー:", err)
		return 1
	}
	fmt.Printf("納入構成の記録を %s に保存しました。\n", *outDir)
	return 0
}

func collect(outDir string) error {
	var systems []operatingSystem
	if err := wmi.Query("SELECT Caption, Version FROM Win32_OperatingSystem", &systems); err != nil {
		return fmt.Errorf("OS情報を読めません: %w", err)
	}
	if len(systems) != 1 || strings.TrimSpace(systems[0].Caption) == "" || systems[0].Version == "" {
		return fmt.Errorf("OS情報を特定できません")
	}
	components := []component{{Type: "operating-system", Name: strings.TrimSpace(systems[0].Caption), Version: systems[0].Version}}
	seen := map[string]bool{}
	for _, path := range []string{`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`} {
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.READ)
		if err != nil {
			return fmt.Errorf("導入済みソフトの登録情報を読めません (%s): %w", path, err)
		}
		names, err := key.ReadSubKeyNames(-1)
		key.Close()
		if err != nil {
			return fmt.Errorf("導入済みソフトの一覧を読めません: %w", err)
		}
		for _, sub := range names {
			k, err := registry.OpenKey(registry.LOCAL_MACHINE, path+`\`+sub, registry.READ)
			if err != nil {
				continue
			} // アンインストール等で列挙中に消えたキー
			name, _, _ := k.GetStringValue("DisplayName")
			version, _, _ := k.GetStringValue("DisplayVersion")
			publisher, _, _ := k.GetStringValue("Publisher")
			k.Close()
			if name == "" {
				continue
			}
			id := name + "\x00" + version
			if seen[id] {
				continue
			}
			seen[id] = true
			components = append(components, component{Type: "application", Name: name, Version: version, Supplier: &supplier{Name: publisher}})
		}
	}
	sort.Slice(components[1:], func(i, j int) bool {
		a, b := components[i+1], components[j+1]
		if a.Name == b.Name {
			return a.Version < b.Version
		}
		return a.Name < b.Name
	})
	var fixes []hotfix
	if err := wmi.Query("SELECT HotFixID, InstalledOn, Description FROM Win32_QuickFixEngineering", &fixes); err != nil {
		return fmt.Errorf("更新プログラムを読めません: %w", err)
	}
	sort.Slice(fixes, func(i, j int) bool { return fixes[i].HotFixID < fixes[j].HotFixID })
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(bom{BOMFormat: "CycloneDX", SpecVersion: "1.5", Version: 1, Components: components}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "system-inventory.cdx.json"), append(data, '\n'), 0644); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(outDir, "windows-updates.csv"))
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.Write([]string{"HotFixID", "InstalledOn", "Description"}); err != nil {
		return err
	}
	for _, fix := range fixes {
		if err := w.Write([]string{fix.HotFixID, fix.InstalledOn, fix.Description}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
