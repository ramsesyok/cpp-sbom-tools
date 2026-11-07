# cpp_deps 仕様（旧 collect_cpp_deps）

> **状態：実装済み（2026-11-07）。** 台帳側の仕様は [ledger_spec.md](ledger_spec.md) を参照。

ビルドPC（Python がない可能性がある）で実行し、ビルドで使った部品の調査結果を `deps.json` に書き出すツール。ビルドPCに持ち込むものを最小にするため、収集だけを行い、サブコマンドはない。人はこのファイルを開かない。別のPCで `cpp_sbom ledger` が読んで Excel の台帳にする。Windows 版の仕様は末尾の「Windows 版」を参照。

実装は `tools/cpp_deps/`（Go）。`sh build.sh` で `dist/` に次のバイナリができる。Windows 向けにビルドすると Windows 版、それ以外では Linux 版の動きをする。

| ファイル | 対象 |
| --- | --- |
| `cpp_deps-linux-amd64` | Linux x86_64 |
| `cpp_deps-linux-arm64` | Linux aarch64（未実行。ビルドのみ確認） |
| `cpp_deps-windows-amd64.exe` | Windows x64 |

Windows で作った Linux 版を持ち込むと実行権限が付いていないので、`chmod +x` してから使う。

## 配布・動作要件

- 単一の静的リンクバイナリ（実行時にライブラリ・ランタイム不要）。`CGO_ENABLED=0` でビルドする。x86_64 必須、aarch64 は任意
- 外部ライブラリは使わない（`excelize` を使うのは `cpp_sbom` だけ。`go version -m` で確認できる）
- 外部通信をしない
- 実行時に使う外部コマンド：対象のコンパイラ（compile_commands.json に書かれたもの）、`ldd`、`dpkg` / `dpkg-query` または `rpm`
- メッセージは日本語。現行と同じ文言を推奨（手順書が「注意:」で始まる行を前提にしているため）

## コマンドライン

```
cpp_deps --compdb <compile_commands.json> --linklog <ビルドログ> --bin <成果物フォルダ> \
                --src <プロジェクトのフォルダ> [--linkdir <フォルダ>] --out <出力ファイル>
```

- `--compdb` `--linklog` `--bin` は少なくとも1つ必須。なければエラー
- `--src` は必須（プロジェクト内のパスを `--src` からの相対パスで記録するため）
- `--out` の既定値は `deps.json`
- `--linkdir`：ビルドログ内の相対パスを解決する基準。既定は `--src`、次にカレントディレクトリ

## 処理

### 1. ヘッダ（--compdb）

- compile_commands.json の各エントリについて、`arguments`（なければ `command` を shell 風に分割）を取り出す
- 次の引数を取り除く：`-o <値>`、`-MF <値>`、`-MT <値>`、`-MQ <値>`、`-c`、`-MD`、`-MMD`、`-M`、`-MM`、`-MP`、`-MG`、`-MF…`／`-o…`（値が連結された形）
- 末尾に `-M` を付け、エントリの `directory` をカレントにして実行する
- 出力の行継続（`\`＋改行）を連結し、各行の `:` より後ろを空白で分割して依存ファイルを得る。相対パスは `directory` 基準で正規化する
- コンパイル対象のソースファイル自身（拡張子 .c/.cc/.cpp/.cxx/.C で、entry の file と同じ実パス）は除く
- 種別「ヘッダ」として記録（同じパスは1回だけ）
- 再実行に失敗したエントリは数え、最後に「注意: N 件のコンパイル命令は再実行できませんでした（結果に含まれません）」と出す

### 2. リンクで使ったライブラリ（--linklog）

GNU ld の `--trace` 出力を読む。ログには make などの他の出力も混ざる。

- 各行を前後の空白を除いて調べる
  - 行全体が 1 つのパスで、`.a` または `.so`（`.so.1.2` のような版番号付きを含む）で終わる → 候補
  - 行中の `(/絶対パス.a)` や `(/絶対パス.so…)` の形（古い ld の形式）→ 候補
  - `.o` は対象外
- 絶対パス：存在すれば採用
- 相対パス：基準フォルダ（`--linkdir`、`--src`、カレント）の順に結合して存在確認。見つからなければ、`--src` 配下の同名ファイルが**ちょうど1つ**ならそれを採用
- それでも見つからないもの → `items` に `unresolved` として出す
- 種別：`.a` は「静的ライブラリ」、それ以外は「リンクした共有ライブラリ」（ヘッダより優先して上書き）
- 1件も見つからなければ「注意: ビルドログにリンクの記録がありません。手順のとおり -Wl,--trace を付けてビルドし直してください」
- `--compdb` があって `--linklog` がない場合は「注意: --linklog が指定されていません。静的ライブラリ（.a）は調べていません」

### 3. 成果物の共有ライブラリ（--bin）

- フォルダを再帰的にたどり、先頭4バイトが ELF マジック（`\x7fELF`）のファイルに `ldd` をかける
- `=> /パス (…)` の右辺、または `/パス (…)` で始まる行のパスを得る
- 種別「共有ライブラリ」（既にあるパスは上書きしない）

### 4. パッケージ逆引き

- `dpkg` があれば dpkg、なければ `rpm`、どちらもなければ警告して逆引きなし
- 問い合わせ候補パス：元のパス、実パス（シンボリックリンク解決）、それぞれの `/usr` 有無を入れ替えたもの（usrmerge 対策）
- dpkg：`dpkg -S <パス>` の出力で `diversion` で始まらず `": "` を含む最初の行から、`": "` より前・カンマより前をパッケージ名とする
  - 版などは `dpkg-query -W -f='${Version}\t${Architecture}\t${source:Package}\t${source:Version}' <pkg>`
- rpm：`rpm -qf --qf '%{NAME}\t%{VERSION}-%{RELEASE}\n' <パス>`（"not owned" を含む場合は不一致）
- 結果はパスごと・パッケージごとにキャッシュする

### 5. 分類

各ファイルについて、上から順に判定する。

1. 実パスが `--src` 配下 → 区分「プロジェクト内」
   - ヘッダ：フォルダ単位に件数を数え、`items` に `project_folder` として1件ずつ出す
   - ヘッダ以外：`items` に `project_library` として出す
2. パッケージが見つかった → 区分「OSパッケージ」
3. それ以外 → 区分「パッケージ外」
   - ヘッダ：フォルダ単位に件数を数え、`items` に `external` として1件ずつ出す（Windows 版と同じ）
   - ヘッダ以外：`items` に `external` として出す

### 6. PURL

- `/etc/os-release` の `ID` と `VERSION_ID` から `distro=<ID>-<VERSION_ID>`
- 版は URL エンコード（`.-~_+` 以外。例：`:` → `%3A`）
- dpkg：`pkg:deb/<ID>/<パッケージ名（:arch を除く）>@<版>?arch=<arch>&distro=<distro>`。ソースパッケージがパッケージ名と異なれば `&upstream=<ソースパッケージ>` を付ける
- rpm：`pkg:rpm/<ID>/<名前>@<版>?distro=<distro>`

### 7. 使用形態

ソースパッケージ単位で「ヘッダ」「ライブラリ」のどちらで使われたかを集計し、各パッケージ行に出す。

- ヘッダだけ → 「ヘッダのみ」（`--linklog` も `--bin` もない場合は「ヘッダ」）
- ライブラリだけ → 「ライブラリのみ」
- 両方 → 「ヘッダとライブラリ」

## 出力（deps.json）

UTF-8（BOM なし）の JSON 1ファイル。Linux 版と Windows 版で同じ形にする。項目がないものは空の配列。

```json
{
  "schema_version": 1,
  "platform": "linux",
  "distro": "ubuntu-24.04",
  "src": "/home/user/mqtt-client",
  "bin_rel": "build",
  "warnings": ["…"],
  "packages": [],
  "items": [],
  "files": [],
  "binaries": [],
  "toolchain": []
}
```

| 項目 | 内容 |
| --- | --- |
| `schema_version` | 形式の版。`cpp_sbom ledger` は知らない版なら停止する |
| `platform` | `linux` / `windows` |
| `distro` | `/etc/os-release` の `<ID>-<VERSION_ID>`。Windows は空 |
| `src` | `--src` の絶対パス（確認用） |
| `bin_rel` | `--bin` の `--src` からの相対パス。`--src` の外か未指定、または `--src` と同じなら空 |
| `warnings` | 画面に出した「注意:」の本文 |

### packages（パッケージ名でソート）

| 項目 | 内容 |
| --- | --- |
| `name` | `libpaho-mqtt-dev:amd64` のようにアーキテクチャ付きのまま |
| `version` | |
| `source` | dpkg の source:Package（なければパッケージ名） |
| `usage` | 7. のとおり |
| `file_count` | 使用ファイル数 |
| `purl` | |

### items（人の判断が必要なもの）

これまでの要確認一覧にあたる。台帳の行のもとになる（まとめ方は ledger_spec.md）。判定は文言ではなく `reason` のコードで行う。

| 項目 | 内容 |
| --- | --- |
| `reason` | 下表のコード |
| `kind` | 画面に出す種別。ヘッダ（N個） / 静的ライブラリ / リンクした共有ライブラリ / 共有ライブラリ / 静的/共有ライブラリ / リンクしたライブラリ / 同梱DLL / 読み込むDLL / 読み取れないEXE/DLL |
| `path` | 調査した環境でのパス（フォルダまたはファイル） |
| `rel_path` | `--src` 配下なら `--src` からの相対パス（区切りは `/`）。それ以外は空 |
| `file_count` | ヘッダのフォルダではヘッダの数。それ以外は 1 |
| `product_name` `product_version` | 同梱DLL のみ。バージョンリソースの値 |

| `reason` | 内容 | Linux | Windows |
| --- | --- | --- | --- |
| `project_folder` | プロジェクト内のヘッダのフォルダ | ○ | ○ |
| `project_library` | プロジェクト内のビルド済みライブラリ | ○ | ○ |
| `external` | OSパッケージに属さない／Microsoft製でもプロジェクト内でもない場所 | ○ | ○ |
| `unresolved` | リンクの記録にあるが場所を特定できない | ○ | |
| `bundled_dll` | 成果物と一緒に配布する DLL | | ○ |
| `unknown_dll` | どこから来るか判別できない DLL | | ○ |
| `unreadable_binary` | EXE/DLL として読み取れない | | ○ |

並び順：`reason` の上表の順 → パス順。

### files（確認用。ファイル1つずつの結果）

| 項目 | 内容 |
| --- | --- |
| `kind` | ヘッダ / 静的ライブラリ / リンクした共有ライブラリ / 共有ライブラリ / 読み込むDLL など |
| `path` | パス（ソート済み） |
| `origin` | OSパッケージ / プロジェクト内 / パッケージ外（Windows は Microsoft / プロジェクト内 / 成果物に同梱 / 別途導入 / 不明） |
| `package` `version` `purl` | `origin` が OSパッケージ のとき |

### binaries（Windows のみ）

`--bin` 配下の EXE/DLL 1つにつき1件。`file`（`--bin` からの相対パス）、`product_name`、`product_version`、`file_version`、`company`、`copyright`。

### toolchain（Windows のみ）

ビルドログのヘッダのパスから読み取る。`name` と `version` を持つ。読み取れないものは出さない。

| `name` | `version` の読み取り元 |
| --- | --- |
| `MSVC` | `…\VC\Tools\MSVC\<版>\…`（複数あれば最も多く現れたもの） |
| `Windows SDK` | `…\Windows Kits\10\Include\<版>\…`（同上） |

## 標準出力

```
[1/4] コンパイル命令 N 件を調べています…
[2/4] リンクで使ったライブラリをビルドログから調べています…
[3/4] 成果物の共有ライブラリを調べています…
[4/4] N 個のファイルの持ち主パッケージを調べています…
注意: …（あれば）
完了しました。
  OSパッケージ: N 個
  人の判断が必要なもの: N 件
  結果: <出力ファイルの絶対パス>
```

`unresolved` が1件以上あれば、「完了しました。」の前に次を出す。この再実行はビルド環境でしかできないので、持ち出す前に気づけるようにするため。

```
注意: 場所を特定できないライブラリが N 件あります。--linkdir にリンクを実行したフォルダを指定して、ここで再実行してください
```

再実行しても解消しない場合は、そのまま持ち出してよい（台帳の行として残り、人が判断する）。終了コードは 0。

## 受け入れテスト

1. `samples/mqtt-client-cmake` を CLAUDE.md の手順でビルドすると、`packages` が13件、`items` が `project_folder` の2件（`src`、`third_party/nlohmann`）になること
2. `samples/mqtt-client-make`（静的リンク）で、`files` に `libpaho-mqtt3c.a` が種別「静的ライブラリ」・`origin`「OSパッケージ」で出ること、`libpaho-mqtt-dev` の `usage` が「ヘッダとライブラリ」になること
3. 同梱の静的ライブラリ（プロジェクト内の `vend/lib/libfoo.a` を `-L vend/lib -lfoo` で相対指定してリンク）が `project_library` として出ること
4. `--linklog` に空のファイルを渡すと「リンクの記録がありません」の注意が出ること
5. 場所を特定できないライブラリがあると `unresolved` と画面の注意が出て、`--linkdir` を付けた再実行で解消すること
6. OSパッケージに属さない場所のヘッダが、ファイルごとではなくフォルダごとに `external` として出ること
7. Python が入っていない環境（最小構成の Ubuntu、Rocky Linux など）で動くこと
8. `testdata/` の expected は Ubuntu 24.04（取得時のパッケージ版）での CSV 出力例。パスと版は環境で変わるので、判定結果の比較元として使う

1〜7 は `tools/cpp_deps/test/acceptance_ubuntu.sh`、rpm 系は `acceptance_rpm.sh` で確認する。単体テストは `go test ./...`。Python 版との比較は、出力形式が変わるのでやめる。

## Windows 版

PowerShell のバージョンや Visual Studio の環境に依存しない（dumpbin も不要）。外部コマンドは実行しない。

### コマンドライン

```
cpp_deps-windows-amd64.exe --bin <成果物フォルダ> --src <プロジェクトのフォルダ> \
                                  --buildlog <ビルドログ> --out <出力ファイル>
```

- `--bin` と `--src` は必須。`--out` の既定値は `deps.json`
- PowerShell 版の `-BinDir` などの別名は廃止する（運用前なので互換性は不要）

### 処理

1. **成果物**：`--bin` 配下の `.exe` `.dll` を再帰的に探し、PE 形式のバージョンリソースから製品名・製品バージョン・ファイルバージョン・会社名・著作権表示を直接読む。言語は VarFileInfo の Translation、英語（`040904B0` `040904E4` `04090000`）、最初の表の順に選ぶ
2. **読み込む DLL**：PE のインポート表と遅延読み込み表から直接読む。`.dll` で終わる名前だけを対象に、大文字小文字を無視して1回だけ記録する。出どころは 成果物に同梱（`--bin` 内に同名がある）→ Microsoft（`api-ms-win-` `vcruntime` などの名前、または `%WINDIR%\System32` にある）→ 不明 の順
3. **ヘッダ**（`--buildlog`）：`including file:` または `インクルード ファイル:` の行から読む
4. **リンクしたライブラリ**（`--buildlog`）：検索中のライブラリの後に「見つかった」行があれば使ったものとする。行頭の `1>` ごとに別々に追跡する

| 内容 | 英語の出力 | 日本語の出力 |
| --- | --- | --- |
| 検索開始 | `Searching <パス>.lib:` | `<パス>.lib を検索中:` |
| 見つかった | `Found <シンボル>` | `<シンボル> が見つかりました。` |
| 検索終了 | `Finished searching libraries` | `ライブラリの検索が終了しました。` |

英語の言語パックが入っていない Visual Studio では `VSLANG=1033` が効かないため、どちらの言語でも読む。ログの文字コードは UTF-8 か Shift_JIS を自動判別する。

### 分類

- ログ内の相対パスは、末尾が一致する `--src` 配下のファイルが**ちょうど1つ**ならそのパスに置き換える。それ以外はそのまま（出どころは「別途導入」になる）
- 出どころは Microsoft（パスに `Windows Kits` `Microsoft Visual Studio` `Microsoft SDKs` のフォルダを含む、または `%WINDIR%` と `Program Files` 配下のそれらのフォルダ）→ プロジェクト内（`--src` 配下）→ 成果物に同梱（`--bin` 配下）→ 別途導入 の順。配下かどうかはフォルダの区切りまで比べる（`C:\proj2` は `C:\proj` の配下ではない）

### 出力

形式は上の「出力（deps.json）」と同じ。`items` の `reason` は次のとおり。

| 対象 | `reason` |
| --- | --- |
| プロジェクト内のヘッダのフォルダ | `project_folder` |
| プロジェクト内のライブラリ | `project_library` |
| 出どころが「別途導入」のヘッダのフォルダ・ライブラリ | `external` |
| 出どころが「成果物に同梱」の DLL | `bundled_dll`（`product_name` `product_version` を入れる） |
| 出どころが「不明」の DLL | `unknown_dll` |
| 読み取れない `.exe` `.dll` | `unreadable_binary` |

### MSVC 以外のコンパイラ

対象は MSVC だけ。MinGW / clang ではヘッダと静的ライブラリを調べられないので、次の手がかりがあれば、`_CL_` などを案内する注意の代わりに「MSVC 以外のコンパイラ…手順書の管理者に相談してください」の注意を出す。終了コードは 0 のまま。

1. ビルドログに `gcc` `g++` `clang` `clang++` `clang-cl` `c++.exe` `cc.exe`（`x86_64-w64-mingw32-` などの接頭辞、`-18` などの版が付くものを含む）のコマンド行がある
2. ログに MSVC の記録が1つもない（または `--buildlog` がない）ときだけ、成果物を調べる。読み込むDLLが `libgcc_s` `libstdc++` `libwinpthread` `libc++` `libunwind` で始まる、または EXE/DLL に文字列 `Mingw-w64 runtime failure` がある

2 を限定するのは、成果物に同梱した他社のDLLが MinGW 製のことがあるため。

### ビルドのしかたごとの確認結果（2026-11-07、Visual Studio 18、Windows PowerShell 5.1）

| ビルドのしかた | 結果 |
| --- | --- |
| CMake（Visual Studio ジェネレータ）+ MSVC、`/fl` でログ | ヘッダ、静的ライブラリ、MSVC と SDK の版を読めた。CMake はライブラリを相対パスで渡すので、同名の `.lib` が `--src` の下に複数あると `external` になる |
| nmake + MSVC、`Out-File -Encoding utf8` でログ | 読めた |
| CMake（NMake ジェネレータ）+ MSVC | ログにコンパイラとリンカの出力が残らず、読めない |
| clang（llvm-mingw）+ コマンドの直接実行 | `--trace` は lld が受け付けない。上の注意が出る |
| CMake（Ninja）、本物の MinGW（GCC） | 未確認 |

Windows PowerShell 5.1 の `>` は UTF-16 で書くので、ログには使わない（`decodeLog` は UTF-8 と Shift_JIS だけを読む）。
