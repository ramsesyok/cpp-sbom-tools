package collect

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"
)

// dumpbin や Windows API に頼らず、EXE / DLL（PE形式）から製品情報と読み込むDLLを直接読み取る。

const (
	dirImport      = 1
	dirResource    = 2
	dirDelayImport = 13
	rtVersion      = 16
)

type versionInfo struct {
	productName, productVersion, fileVersion, companyName, legalCopyright string
}

type peInfo struct {
	version versionInfo
	imports []string // 遅延読み込みのDLLを含む
}

type dataDir struct{ rva, size uint32 }

type peImage struct {
	sections  []*pe.Section
	data      map[*pe.Section][]byte
	dirs      []dataDir
	imageBase uint64
}

func readPE(path string) (peInfo, error) {
	f, err := pe.Open(path)
	if err != nil {
		return peInfo{}, err
	}
	defer f.Close()
	img := &peImage{sections: f.Sections, data: map[*pe.Section][]byte{}}
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		img.imageBase = uint64(oh.ImageBase)
		for _, d := range oh.DataDirectory {
			img.dirs = append(img.dirs, dataDir{d.VirtualAddress, d.Size})
		}
	case *pe.OptionalHeader64:
		img.imageBase = oh.ImageBase
		for _, d := range oh.DataDirectory {
			img.dirs = append(img.dirs, dataDir{d.VirtualAddress, d.Size})
		}
	default:
		return peInfo{}, errors.New("実行形式ではありません")
	}
	return peInfo{version: img.versionInfo(), imports: img.importedDLLs()}, nil
}

// at は rva から最大 n バイトを返す（セクションの終わりで打ち切る）。
func (img *peImage) at(rva uint32, n uint32) []byte {
	for _, s := range img.sections {
		span := s.VirtualSize
		if s.Size > span {
			span = s.Size
		}
		if rva < s.VirtualAddress || rva-s.VirtualAddress >= span {
			continue
		}
		b, ok := img.data[s]
		if !ok {
			b, _ = s.Data()
			img.data[s] = b
		}
		off := rva - s.VirtualAddress
		if off >= uint32(len(b)) {
			return nil
		}
		b = b[off:]
		if uint32(len(b)) > n {
			b = b[:n]
		}
		return b
	}
	return nil
}

func (img *peImage) cstring(rva uint32) string {
	b := img.at(rva, 1024)
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func (img *peImage) dir(i int) dataDir {
	if i < len(img.dirs) {
		return img.dirs[i]
	}
	return dataDir{}
}

func (img *peImage) importedDLLs() []string {
	const maxEntries = 65536
	var names []string
	if d := img.dir(dirImport); d.rva != 0 {
		for i := uint32(0); i < maxEntries; i++ {
			e := img.at(d.rva+i*20, 20)
			if len(e) < 20 {
				break
			}
			nameRVA := binary.LittleEndian.Uint32(e[12:])
			if nameRVA == 0 {
				break
			}
			if s := img.cstring(nameRVA); s != "" {
				names = append(names, s)
			}
		}
	}
	if d := img.dir(dirDelayImport); d.rva != 0 {
		for i := uint32(0); i < maxEntries; i++ {
			e := img.at(d.rva+i*32, 32)
			if len(e) < 32 {
				break
			}
			attrs := binary.LittleEndian.Uint32(e[0:])
			nameRVA := binary.LittleEndian.Uint32(e[4:])
			if nameRVA == 0 {
				break
			}
			// 古い形式（属性の最下位ビットが0）は RVA ではなく仮想アドレスで書かれている
			if attrs&1 == 0 {
				nameRVA = uint32(uint64(nameRVA) - img.imageBase)
			}
			if s := img.cstring(nameRVA); s != "" {
				names = append(names, s)
			}
		}
	}
	return names
}

// versionResource はリソースの種類 RT_VERSION の最初のデータを返す。
func (img *peImage) versionResource() []byte {
	d := img.dir(dirResource)
	if d.rva == 0 {
		return nil
	}
	rsrc := img.at(d.rva, d.size)
	// 種類 → 名前 → 言語 の3段。名前と言語は最初のものを使う
	off, isDir, ok := resourceEntry(rsrc, 0, rtVersion, true)
	if !ok || !isDir {
		return nil
	}
	if off, isDir, ok = resourceEntry(rsrc, off, 0, false); !ok || !isDir {
		return nil
	}
	if off, isDir, ok = resourceEntry(rsrc, off, 0, false); !ok || isDir {
		return nil
	}
	if uint64(off)+16 > uint64(len(rsrc)) {
		return nil
	}
	dataRVA := binary.LittleEndian.Uint32(rsrc[off:])
	size := binary.LittleEndian.Uint32(rsrc[off+4:])
	return img.at(dataRVA, size)
}

// resourceEntry はリソースディレクトリから項目を探す。byID が false なら最初の項目を返す。
func resourceEntry(rsrc []byte, dirOff, id uint32, byID bool) (off uint32, isDir, ok bool) {
	if uint64(dirOff)+16 > uint64(len(rsrc)) {
		return 0, false, false
	}
	named := uint32(binary.LittleEndian.Uint16(rsrc[dirOff+12:]))
	ids := uint32(binary.LittleEndian.Uint16(rsrc[dirOff+14:]))
	for i := uint32(0); i < named+ids; i++ {
		e := uint64(dirOff) + 16 + uint64(i)*8
		if e+8 > uint64(len(rsrc)) {
			return 0, false, false
		}
		nameID := binary.LittleEndian.Uint32(rsrc[e:])
		target := binary.LittleEndian.Uint32(rsrc[e+4:])
		if !byID || nameID == id {
			return target &^ 0x80000000, target&0x80000000 != 0, true
		}
	}
	return 0, false, false
}

func (img *peImage) versionInfo() versionInfo {
	return parseVersionResource(img.versionResource())
}

// versionBlock は VS_VERSIONINFO の入れ子構造の1要素。body はキーの後ろからブロックの終わりまで。
type versionBlock struct {
	key  string
	body []byte
}

func align4(n int) int { return (n + 3) &^ 3 }

func utf16String(b []byte) (s string, consumed int) {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			return string(utf16.Decode(u)), i + 2
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u)), len(b)
}

// splitBlocks は並んだブロックを順に切り出す。
func splitBlocks(b []byte) []versionBlock {
	var blocks []versionBlock
	for len(b) >= 6 {
		length := int(binary.LittleEndian.Uint16(b))
		if length < 6 || length > len(b) {
			break
		}
		key, n := utf16String(b[6:length])
		bodyStart := align4(6 + n)
		if bodyStart > length {
			bodyStart = length
		}
		blocks = append(blocks, versionBlock{key, b[bodyStart:length]})
		next := align4(length)
		if next > len(b) {
			break
		}
		b = b[next:]
	}
	return blocks
}

func parseVersionResource(data []byte) versionInfo {
	var v versionInfo
	root := splitBlocks(data)
	if len(root) == 0 {
		return v
	}
	// 先頭は固定長の VS_FIXEDFILEINFO（52バイト）
	body := root[0].body
	if len(body) < 52 {
		return v
	}
	var translation string
	var tables []versionBlock
	for _, c := range splitBlocks(body[52:]) {
		switch c.key {
		case "StringFileInfo":
			tables = append(tables, splitBlocks(c.body)...)
		case "VarFileInfo":
			for _, vb := range splitBlocks(c.body) {
				if vb.key == "Translation" && len(vb.body) >= 4 && translation == "" {
					translation = hex4(binary.LittleEndian.Uint16(vb.body)) + hex4(binary.LittleEndian.Uint16(vb.body[2:]))
				}
			}
		}
	}
	table := pickStringTable(tables, translation)
	if table == nil {
		return v
	}
	for _, s := range splitBlocks(table.body) {
		value, _ := utf16String(s.body)
		switch s.key {
		case "ProductName":
			v.productName = value
		case "ProductVersion":
			v.productVersion = value
		case "FileVersion":
			v.fileVersion = value
		case "CompanyName":
			v.companyName = value
		case "LegalCopyright":
			v.legalCopyright = value
		}
	}
	return v
}

// pickStringTable は宣言された言語の表、英語の表、最初の表の順に選ぶ（.NET の FileVersionInfo に合わせる）。
func pickStringTable(tables []versionBlock, translation string) *versionBlock {
	for _, want := range []string{translation, "040904B0", "040904E4", "04090000"} {
		for i := range tables {
			if want != "" && strings.EqualFold(tables[i].key, want) {
				return &tables[i]
			}
		}
	}
	if len(tables) > 0 {
		return &tables[0]
	}
	return nil
}

func hex4(v uint16) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[v>>12], digits[v>>8&15], digits[v>>4&15], digits[v&15]})
}
