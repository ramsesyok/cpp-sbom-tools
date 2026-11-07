package collect

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ---------- OS情報 ----------
func parseOSRelease(text string) map[string]string {
	info := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			info[k] = strings.Trim(v, `"`)
		}
	}
	return info
}

// ---------- ヘッダの洗い出し ----------
type compileEntry struct {
	Arguments []string `json:"arguments"`
	Command   string   `json:"command"`
	Directory string   `json:"directory"`
	File      string   `json:"file"`
}

// shellSplit は command をシェルと同じ規則で引数に分割する（Python の shlex.split 相当）。
func shellSplit(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inToken := false
	var quote rune
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			// 二重引用符の中で \ が効くのは " と \ だけ
			if quote == '"' && r != '"' && r != '\\' {
				cur.WriteByte('\\')
			}
			cur.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			if r == '"' {
				quote = 0
			} else if r == '\\' {
				escaped = true
			} else {
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped, inToken = true, true
		case r == '\'' || r == '"':
			quote, inToken = r, true
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			if inToken {
				args = append(args, cur.String())
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if quote != 0 || escaped {
		return nil, errors.New("引用符または \\ が閉じていません")
	}
	if inToken {
		args = append(args, cur.String())
	}
	return args, nil
}

func entryArgs(e compileEntry) ([]string, error) {
	if e.Arguments != nil {
		return e.Arguments, nil
	}
	return shellSplit(e.Command)
}

// dependencyArgs は出力先などの指定を除き、末尾に -M を付ける。
func dependencyArgs(args []string) []string {
	out := []string{args[0]}
	skip := false
	for _, a := range args[1:] {
		if skip {
			skip = false
			continue
		}
		switch a {
		case "-o", "-MF", "-MT", "-MQ":
			skip = true
			continue
		case "-c", "-MD", "-MMD", "-M", "-MM", "-MP", "-MG":
			continue
		}
		if strings.HasPrefix(a, "-MF") || strings.HasPrefix(a, "-o") {
			continue
		}
		out = append(out, a)
	}
	return append(out, "-M")
}

func parseDepOutput(stdout, cwd string) []string {
	var files []string
	for _, line := range strings.Split(strings.ReplaceAll(stdout, "\\\n", " "), "\n") {
		_, deps, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		for _, d := range strings.Fields(deps) {
			if filepath.IsAbs(d) {
				files = append(files, d)
			} else {
				files = append(files, filepath.Join(cwd, d))
			}
		}
	}
	return files
}

// headersFromEntry は1つのコンパイル命令に -M を付けて再実行し、読み込まれたファイル一覧を得る。
func headersFromEntry(e compileEntry) ([]string, bool) {
	args, err := entryArgs(e)
	if err != nil || len(args) == 0 {
		return nil, false
	}
	args = dependencyArgs(args)
	cwd := e.Directory
	if cwd == "" {
		cwd = "."
	}
	rc, stdout := runCmd(cwd, args[0], args[1:]...)
	if rc != 0 {
		return nil, false
	}
	return parseDepOutput(stdout, cwd), true
}

func isSourceFile(p string) bool {
	for _, ext := range []string{".c", ".cc", ".cpp", ".cxx", ".C"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

// realPath はシンボリックリンクを解決した絶対パスを返す。存在しない部分はそのまま残す。
func realPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	cur, rest := abs, ""
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---------- 共有ライブラリの洗い出し ----------
func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return string(head) == "\x7fELF"
}

func parseLdd(stdout string) []string {
	var libs []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if _, right, ok := strings.Cut(line, "=>"); ok {
			right = strings.TrimSpace(right)
			if strings.HasPrefix(right, "/") {
				path, _, _ := strings.Cut(right, " (")
				libs = append(libs, path)
			}
		} else if strings.HasPrefix(line, "/") {
			path, _, _ := strings.Cut(line, " (")
			libs = append(libs, path)
		}
	}
	return libs
}

// ---------- リンクで使ったライブラリ（-Wl,--trace の出力） ----------
// Go の \s は ASCII だけなので、全角空白なども空白として扱う
const spaceChars = `\s\x0b\x1c-\x1f\x{85}\x{A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}`

var (
	libLine  = regexp.MustCompile(`^([^` + spaceChars + `]+\.(?:a|so(?:\.[0-9]+)*))$`)
	libParen = regexp.MustCompile(`\((/[^` + spaceChars + `()]+\.(?:a|so(?:\.[0-9]+)*))\)`)
)

// parseLinkLine はログの1行からライブラリのパス候補を取り出す。
func parseLinkLine(line string) []string {
	line = strings.TrimSpace(line)
	var cands []string
	for _, m := range libParen.FindAllStringSubmatch(line, -1) {
		cands = append(cands, m[1])
	}
	if libLine.MatchString(line) {
		cands = append(cands, line)
	}
	return cands
}

// indexLibs はプロジェクト内の .a / .so をファイル名で引けるようにする（相対パス対策）。
func indexLibs(root string) map[string][]string {
	idx := map[string][]string{}
	if root == "" {
		return idx
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".svn" {
				return filepath.SkipDir
			}
			return nil
		}
		if libLine.MatchString(d.Name()) {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				return nil
			}
			idx[d.Name()] = append(idx[d.Name()], p)
		}
		return nil
	})
	return idx
}

func libsFromLinkLog(path string, bases []string, srcIdx map[string][]string) (found, unresolved []string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	for _, line := range strings.Split(normalizeNewlines(string(data)), "\n") {
		for _, c := range parseLinkLine(line) {
			if filepath.IsAbs(c) {
				if exists(c) {
					found = append(found, filepath.Clean(c))
				}
				continue
			}
			hit := ""
			for _, b := range bases {
				if b != "" && exists(filepath.Join(b, c)) {
					hit = filepath.Join(b, c)
					break
				}
			}
			if hit == "" {
				if same := srcIdx[filepath.Base(c)]; len(same) == 1 {
					hit = same[0]
				}
			}
			if hit == "" {
				unresolved = append(unresolved, c)
				continue
			}
			if abs, err := filepath.Abs(hit); err == nil {
				hit = abs
			}
			found = append(found, filepath.Clean(hit))
		}
	}
	return found, unresolved, nil
}

func libKind(path string) string {
	if strings.HasSuffix(path, ".a") {
		return "静的ライブラリ"
	}
	return "リンクした共有ライブラリ"
}

// ---------- パッケージ逆引き ----------
type pkgInfo struct {
	name, version, arch, source string
}

type pkgDB struct {
	kind   string
	cache  map[string]pkgInfo
	vcache map[string]pkgInfo
}

func newPkgDB() *pkgDB {
	db := &pkgDB{cache: map[string]pkgInfo{}, vcache: map[string]pkgInfo{}}
	if _, err := exec.LookPath("dpkg"); err == nil {
		db.kind = "dpkg"
	} else if _, err := exec.LookPath("rpm"); err == nil {
		db.kind = "rpm"
	}
	return db
}

// ownerCandidates は問い合わせるパスの候補を返す。/usr の有無も入れ替える（usrmerge 対策）。
func ownerCandidates(path, real string) []string {
	c := []string{path}
	if real != path {
		c = append(c, real)
	}
	for _, p := range append([]string(nil), c...) {
		if strings.HasPrefix(p, "/usr/") {
			c = append(c, p[4:])
		} else {
			c = append(c, "/usr"+p)
		}
	}
	return c
}

func parseDpkgOwner(stdout string) string {
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, ": ") && !strings.HasPrefix(line, "diversion") {
			pkg, _, _ := strings.Cut(line, ": ")
			pkg, _, _ = strings.Cut(pkg, ",")
			return strings.TrimSpace(pkg)
		}
	}
	return ""
}

func (db *pkgDB) owner(path string) pkgInfo {
	if info, ok := db.cache[path]; ok {
		return info
	}
	var res pkgInfo
	for _, c := range ownerCandidates(path, realPath(path)) {
		if db.kind == "dpkg" {
			rc, stdout := runCmd("", "dpkg", "-S", c)
			if rc != 0 || stdout == "" {
				continue
			}
			if pkg := parseDpkgOwner(stdout); pkg != "" {
				res = db.dpkgInfo(pkg)
				break
			}
		} else if db.kind == "rpm" {
			rc, stdout := runCmd("", "rpm", "-qf", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\n", c)
			if rc != 0 || stdout == "" || strings.Contains(stdout, "not owned") {
				continue
			}
			first, _, _ := strings.Cut(stdout, "\n")
			if name, ver, ok := strings.Cut(first, "\t"); ok {
				res = pkgInfo{name: name, version: ver}
				break
			}
		}
	}
	db.cache[path] = res
	return res
}

func (db *pkgDB) dpkgInfo(pkg string) pkgInfo {
	if info, ok := db.vcache[pkg]; ok {
		return info
	}
	info := pkgInfo{name: pkg}
	rc, stdout := runCmd("", "dpkg-query", "-W", "-f=${Version}\t${Architecture}\t${source:Package}\t${source:Version}", pkg)
	if s := strings.TrimSpace(stdout); rc == 0 && s != "" {
		f := append(strings.Split(s, "\t"), "", "", "")
		info.version, info.arch, info.source = f[0], f[1], f[2]
	}
	db.vcache[pkg] = info
	return info
}

// purlEscape は Python の urllib.parse.quote(ver, safe=".-~_+") と同じ変換をする。
func purlEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(".-~_+", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func makePURL(kind string, osinfo map[string]string, info pkgInfo) string {
	osID := osinfo["ID"]
	if osID == "" {
		osID = "linux"
	}
	distro := osID + "-" + osinfo["VERSION_ID"]
	base, _, _ := strings.Cut(info.name, ":")
	ver := purlEscape(info.version)
	switch kind {
	case "dpkg":
		q := "?distro=" + distro
		if info.arch != "" {
			q = "?arch=" + info.arch + "&distro=" + distro
		}
		if info.source != "" && info.source != base {
			q += "&upstream=" + info.source
		}
		return "pkg:deb/" + osID + "/" + base + "@" + ver + q
	case "rpm":
		return "pkg:rpm/" + osID + "/" + base + "@" + ver + "?distro=" + distro
	}
	return ""
}

func useForm(use map[string]bool, hasLibInput bool) string {
	switch {
	case use["ヘッダ"] && use["ライブラリ"]:
		return "ヘッダとライブラリ"
	case use["ライブラリ"]:
		return "ライブラリのみ"
	case hasLibInput:
		return "ヘッダのみ"
	}
	return "ヘッダ"
}

type pkgSummary struct {
	version, source, purl string
	files                 int
}
