package makeledger

import (
	"os"
	"path/filepath"
	"testing"

	"cpp_deps/internal/deps"
	"cpp_deps/internal/ledger"
)

func TestSystemInventoryRequiresDeliveredOS(t *testing.T) {
	write := func(s string) string {
		p := filepath.Join(t.TempDir(), "inventory.json")
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	linux := write(`{"bomFormat":"CycloneDX","components":[{"name":"libssl3","version":"3.0","purl":"pkg:deb/ubuntu/libssl3@3.0?distro=ubuntu-24.04"}]}`)
	rows, err := readSystemInventory(linux)
	if err != nil || len(rows) != 2 || rows[0].Get(ledger.ColKind) != ledger.KindSystemOS || rows[1].Get(ledger.ColKind) != ledger.KindOSPackage {
		t.Fatalf("Linux inventory: %+v, %v", rows, err)
	}
	windows := write(`{"bomFormat":"CycloneDX","components":[{"type":"operating-system","name":"Windows 11","version":"10.0.1"},{"type":"application","name":"Vendor App","version":"2","supplier":{"name":"Vendor"}}]}`)
	rows, err = readSystemInventory(windows)
	if err != nil || len(rows) != 2 || rows[1].Get(ledger.ColKind) != ledger.KindInstalled || rows[1].Get(ledger.ColHolders) != "Vendor" {
		t.Fatalf("Windows inventory: %+v, %v", rows, err)
	}
	if _, err := readSystemInventory(write(`{"bomFormat":"CycloneDX","components":[{"name":"app","version":"1"}]}`)); err == nil {
		t.Fatal("納入OSのない調査結果を受け入れました")
	}
}

func TestProductRowsSeparateBuildAndDelivery(t *testing.T) {
	r := &deps.Result{Packages: []deps.Package{
		{Name: "static:amd64", Version: "1", PURL: "pkg:deb/ubuntu/static@1"},
		{Name: "runtime:amd64", Version: "2", PURL: "pkg:deb/ubuntu/runtime@2"},
		{Name: "headers:amd64", Version: "3", PURL: "pkg:deb/ubuntu/headers@3"},
	}, Files: []deps.File{
		{Kind: "静的ライブラリ", Path: "/usr/lib/libstatic.a", Package: "static:amd64"},
		{Kind: "共有ライブラリ", Path: "/usr/lib/libruntime.so", Package: "runtime:amd64"},
		{Kind: "ヘッダ", Path: "/usr/include/headers.h", Package: "headers:amd64"},
	}}
	rows := []ledger.Row{
		{ledger.ColKind: ledger.KindOSPackage, ledger.ColName: "static", ledger.ColVersion: "1", ledger.ColPURL: "pkg:deb/ubuntu/static@1"},
		{ledger.ColKind: ledger.KindOSPackage, ledger.ColName: "runtime", ledger.ColVersion: "2", ledger.ColPURL: "pkg:deb/ubuntu/runtime@2"},
		{ledger.ColKind: ledger.KindOSPackage, ledger.ColName: "headers", ledger.ColVersion: "3", ledger.ColPURL: "pkg:deb/ubuntu/headers@3", ledger.ColNote: "ヘッダのみ"},
		{ledger.ColKind: ledger.KindMicrosoft, ledger.ColName: "MSVC"},
	}
	got := productRows(rows, r, map[string]bool{})
	if len(got) != 2 || got[0].Get(ledger.ColKind) != ledger.KindBundled || got[1].Get(ledger.ColKind) != "" {
		t.Fatalf("製品行: %+v", got)
	}
}
