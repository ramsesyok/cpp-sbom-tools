# 部品台帳 仕様 — cpp_sbom（旧仕様と検証履歴）

> 製品種別による現行の範囲、入力、判定は `docs/product_scope_spec.md` を参照。この文書の以下のコマンドと受け入れ試験は変更前の記録として残す。

> **状態：実装済み（2026-11-07）。** 手順書も切り替え済みで、古いスクリプトは削除した。受け入れテストの結果は末尾を参照。

調査結果を Excel の台帳1冊にまとめ、人が記入する場所をそこだけにする。`make_ledger.py`、`ledger_to_cyclonedx.py`、`scancode_summary.py`、`deps_unknown.csv`、CSV の台帳は廃止した。

PCの区切りに合わせて、バイナリを2つに分ける。ビルドPCには収集だけを持ち込み、それ以降は別のPC（手順書では Windows の作業PC）で行う。

| バイナリ | サブコマンド | 担当の範囲 | 置き換えるもの |
| --- | --- | --- | --- |
| `cpp_deps` | なし | ビルドPCでの調査（`deps.json` を作る） | `collect_cpp_deps` |
| `cpp_sbom` | `ledger`、`check`、`convert` | 作業PCで台帳を作り、完成を確かめ、SBOM にする | `make_ledger.py`、`scancode_summary.py`、`ledger_to_cyclonedx.py` |

## 流れ

| 順 | 場所 | 作業 | 人の判断 |
| --- | --- | --- | --- |
| 1 | ビルドPC | `cpp_deps` で `deps.json` を作る（Linux は Syft で `build-env.cdx.json` も作る。Syft はインストールせず tar.gz を展開して使う） | 画面の注意に従って再実行 |
| 2 | ビルドPC → 作業PC | `deps.json`、`build-env.cdx.json`（Linux）、ビルドした状態のプロジェクト全体（`source.tar.gz`）を運ぶ | なし |
| 3 | 作業PC | プロジェクト全体を ScanCode にかける | なし |
| 4 | 作業PC | `cpp_sbom ledger` で `部品台帳.xlsx` を作る | なし |
| 5 | 作業PC | 台帳の色の付いたセルを埋める | **ここだけ** |
| 6 | 作業PC | `cpp_sbom check` で台帳の完成を確かめる | 表示された行を直す |
| 7 | 作業PC | `cpp_sbom convert` で SBOM に変換し、`cyclonedx validate` で形式チェック | なし |

ScanCode は `--src` に渡したフォルダそのものを調べる。

```
scancode -clpi -n 4 --json-pp scancode.json <プロジェクトのフォルダ>
```

## 動作要件

- どちらもバイナリだけで動く。Python は不要（ScanCode と cve-bin-tool 自体には引き続き必要）。`cpp_deps` は外部ライブラリも含まない
- どの OS の `deps.json` も処理できる（Linux の結果を Windows の作業PCで台帳にしてよい）
- 外部通信をしない。メッセージは日本語

## ビルドと配布

- 実装は `tools/cpp_deps/` の1つの Go モジュールにまとめ、`sh build.sh` で2つのバイナリを `dist/` に作る
- 台帳の列と「必須の項目」の定義は1か所（`internal/ledger/`）に置く（台帳の色付けと変換時のチェックが食い違わないようにするため）
- `cpp_sbom` の出力ファイル名は `cpp_sbom-windows-amd64.exe`（手順書で使う）、`cpp_sbom-linux-amd64`、`cpp_sbom-linux-arm64`
- Excel の読み書きに `github.com/xuri/excelize/v2` を使う。ツール自身が使うだけで、製品の台帳と SBOM には載らない
- ビルドはネット接続PCで行う。作業PCとビルドPCには完成したバイナリだけを持ち込む
- 依存ライブラリの版は `go.mod` と `go.sum` で固定する。更新するときはビルドし直して配布する
- 配布物に、`excelize` と依存先のライセンス文を `THIRD_PARTY_LICENSES.txt` として添える（`build.sh` で `dist/` に作る）
- 使っているライブラリは `go version -m <バイナリ>` で確認できる
- 現在の版は `excelize` v2.11.1。`CGO_ENABLED=0` でのクロスビルドが通ることを確認済み

## cpp_sbom ledger

```
cpp_sbom ledger --deps deps.json --scan scancode.json --src <プロジェクトのフォルダ> \
                [--osenv build-env.cdx.json] [--out 部品台帳.xlsx]
```

- `--deps` `--scan` `--src` は必須。`--osenv` は `platform` が `linux` のとき必須、`windows` では使わない
- `--src` はバージョン候補を読むために使う
- 出力先がすでにあれば、上書きせずに停止する（手作業の記入を守るため）
- `deps.json` の `schema_version` を知らなければ停止する
- `deps.json` の `warnings` を画面に再掲する

### ScanCode の結果とフォルダの対応

- ScanCode の `path` の先頭要素（調べたフォルダの名前）を除いたものを、`--src` からの相対パスとして扱う。`platform` が `windows` なら大文字小文字を区別しない
- `project_folder` のフォルダのうち、ScanCode の結果にあるものが1つもなければ、「ScanCode で調べたフォルダと --src が違います」と出して停止する
- フォルダのライセンスと著作権者は、**そのフォルダの直下のファイル**から集める（サブフォルダは含めない）。ライセンスは検出結果をそのまま使う。まとめ方の定義は `internal/makeledger/scan.go`

### 行の作り方

**1. OSパッケージ（`packages`）**

1件1行。部品名はアーキテクチャを除いた名前。ライセンスは `--osenv` から引く。

**2. プロジェクト内のフォルダ**

次のどちらかにあたるフォルダを候補にする。

- `items` の `project_folder`
- 直下のファイルからライセンスか著作権者が検出されたフォルダ。ただし `.git` `.svn` と `bin_rel` の配下は除く

候補のうち、親のフォルダも候補で、**両方にライセンスと著作権者が検出され、それぞれ完全に一致する**ものは親の行にまとめる。何も検出されなかったフォルダはまとめない（自社フォルダの中に、ライセンス表記のない OSS が隠れるのを防ぐため）。

部品名の初期値はフォルダ名、バージョンの初期値はバージョン候補の先頭。バージョン候補は、そのフォルダの直下のヘッダを `internal/makeledger/scan.go` の正規表現で読む。

**3. その他の `items`**

1件1行。`bundled_dll` は `product_name` を部品名、`product_version` をバージョンの初期値にする。

**4. Microsoft（`toolchain`）**

`platform` が `windows` のとき、1件1行。`toolchain` に Windows SDK か MSVC がなければ、部品名だけ入れてバージョンを空にする。

### 区分の初期値と「やること」

| もとの結果 | 区分の初期値 | やること（固定の文言） |
| --- | --- | --- |
| `packages` | OSパッケージ | ライセンスが空なら記入 |
| `toolchain` | Microsoft | バージョンが空なら開発担当者に確認して記入 |
| `project_folder`、ライセンス検出フォルダ | 空 | 自社で書いたコードだけなら区分を「対象外」に。OSS を含むなら「同梱」にして色の付いたセルを記入 |
| `project_library` | 空 | 自社開発（OSS を含まない）なら「対象外」に。OSS を含むなら自社でビルドしたものも「同梱」にして記入 |
| `bundled_dll` | 空 | 同上 |
| `external` | 別途導入 | 部品名・バージョンを開発担当者に確認して記入 |
| `unknown_dll`、`unreadable_binary` | 別途導入 | 開発担当者に入手元を確認して記入 |
| `unresolved` | 空 | ビルドPCで --linkdir を付けて再実行しても残ったもの。開発担当者に確認し、区分を選んで記入 |

### 行の並び

人が記入する行を上に置く。区分が空の行 → 別途導入 → Microsoft → OSパッケージ。それぞれの中は「場所」または部品名の順。

### 画面の表示

```
完了しました。部品 N 行を 部品台帳.xlsx に書き出しました（記入が必要な行 M 行）。
```

## cpp_sbom check

```
cpp_sbom check --ledger 部品台帳.xlsx [--cvebin cvebin-parts.json]
```

記入のあとに台帳の完成を確かめる。

- `--cvebin` を付けると、作業4の cve-bin-tool の結果（`--sbom-output`）の部品が台帳にあるかも確かめる（下の「作業4の結果との照合」）。`--cvebin` は `convert` にはない。cve-bin-tool の結果を SBOM にマージしないのは、OS パッケージと二重に載り、未確認の部品が必須項目のチェックを通らずに入るため
- 判定は `cpp_sbom convert` と同じコードで行う。必須の項目のほか、CPE の食い違いも止める対象にする。check に合格した台帳は変換で止まらない
- ファイルは何も書き出さない
- 足りない項目は「12行目（同梱）: ライセンスが未記入です」の形ですべて表示する。注意（CPE も PURL もない、PURL の食い違い、複数の OS の混在）も、完成・未完成にかかわらず表示する
- 注意だけなら完成として扱う
- 台帳を Excel で開いたままなら、変換と同じ注意を出す
- 終了コード：0 = 完成、1 = 足りない項目がある、または読めない、2 = 指定の誤り

完成のときの表示：

```
台帳は完成しています。SBOM に入る部品は N 件です。「対象外」は M 行です。
```

### 作業4の結果との照合

部品ごとに、台帳のすべての行を次の順で見る。

| 判定 | 条件 | 結果 |
| --- | --- | --- |
| 行が部品にあたる | CPE製品名が一致する。または、部品名、CPE製品名、PURL の `upstream=` を正規化した名前が一致する | 下の判定へ |
| 確認済み | あたる行のバージョンが同じ。OS パッケージの `1:1.3.dfsg-3` は `1.3` と同じとみなす | 表示だけ |
| 確認済み（対象外） | 同じ版の行はないが、区分が「対象外」のあたる行がある。誤検出と判断した記録として扱う | 表示だけ |
| 要確認 | あたる行はあるが、バージョンが違う | 終了コード 1 |
| 要確認 | あたる行がない | 終了コード 1 |

- 正規化は `lib` と末尾の `-dev`、`-libs`、`t64`、数字、`.dll` などを取る。`openssl` は `ssl` と `crypto` にもあたる
- 部品ではない `CVEBINTOOL-<フォルダ名>` は除く。これが1つもないファイルは cve-bin-tool の結果ではないとして止める（製品の SBOM を指定すると、すべて一致して見えるため）
- 部品が0件なら合格にするが、漏れがないという意味ではないと表示する
- 台帳の未記入と要確認は、1回でまとめて表示する

## 台帳（部品台帳.xlsx）

### シート「部品台帳」

| 列 | 記入 | SBOM に入るか | 内容 |
| --- | --- | --- | --- |
| 区分 | 選ぶ | ○ | OSパッケージ / 同梱 / 別途導入 / Microsoft / 対象外 |
| 部品名 | 記入 | ○ | |
| バージョン | 記入 | ○ | |
| ライセンス | 記入 | ○ | |
| CPEベンダー | 記入 | ○ | 調べても CPE がないときは「なし」と書く |
| CPE製品名 | 記入 | ○ | |
| PURL | 記入 | ○ | |
| 確認者 | 記入 | ○ | |
| 備考 | 記入 | ○ | 対象外の理由もここに書く |
| 場所 | 参照 | × | フォルダまたはファイル。社内のパスを SBOM に出さない |
| 著作権者 | 参照 | × | 多い順に最大5件 |
| バージョン候補 | 参照 | × | 最大3件 |
| やること | 参照 | × | 上の表の文言 |

Excel の設定：

- すべてのセルを文字列の書式にする（`1.10` が `1.1` に変わるのを防ぐ）
- 区分はプルダウン。一覧にない値は入力できない
- 必須の項目が空のセルに色を付ける。埋めると色が消える（条件付き書式。「必須の項目」の表と同じ条件）
- 見出し行の固定とフィルタ。参照の列は灰色

人が行を追加してもよい（自動で検出できなかった部品を見つけた場合）。

### シート「調査結果」

確認用。変換では読まない。`deps.json` の `files` と `binaries` を次の列で1つの表にする。

種類、ファイル、出どころ、パッケージ／製品名、バージョン、PURL、会社名、著作権表示

### 必須の項目

| 区分 | 必須 |
| --- | --- |
| （空） | 区分を選ぶまで変換しない |
| OSパッケージ | 部品名、バージョン、ライセンス |
| 同梱、別途導入 | 部品名、バージョン、ライセンス、CPE（ベンダーと製品名の両方。CPE がなければベンダーに「なし」）または PURL、確認者 |
| Microsoft | 部品名、バージョン |
| 対象外 | 確認者、備考（理由） |

「要確認」列はなくす。確認が必要な値（バージョンの初期値など）は、確認者の記入をもって確認済みとする。OSパッケージの行も、製品に載せないと決めたものは「対象外」にできる。

## cpp_sbom convert

```
cpp_sbom convert --ledger 部品台帳.xlsx --name <製品名> --version <製品バージョン> [--out product.cdx.json] [--allow-pending]
```

CPE のエスケープ、PURL の補完、JSON の形の定義は `internal/tocdx/tocdx.go`。

- シート「部品台帳」を見出し名で読む。セルの値は文字列として扱う
- 必須の項目が欠けた行があれば変換せず、「12行目（同梱）: ライセンスが未記入です」の形ですべて表示して停止する。`--allow-pending` はその行を飛ばして変換する（試行用）
- 区分「対象外」の行は SBOM に入れない。入れなかった行数を画面に出す
- 部品名とバージョンが同じ行は1つの部品にまとめる
  - ライセンスはフォルダのまとめ方と同じ方法で合成する
  - CPE が食い違えば、行番号を出して停止する
  - PURL が食い違えば、最初の行のものを使って注意を出す
- CPEベンダーが「なし」なら CPE を付けない。PURL も空なら `pkg:generic` を付ける（現在と同じ）
- 区分「Microsoft」の行には、CPE も PURL もないという注意を出さない（手順書の「無視してよい」をなくすため）
- operating-system コンポーネントの自動付与、複数の OS が混在したときの注意は現在と同じ
- 台帳を Excel で開いたままなら（`~$` で始まる一時ファイルがあれば）、「保存していない変更は読めません」と注意を出す

## 受け入れテスト

1. `samples/mqtt-client-cmake`（Linux）で台帳を作ると、OSパッケージ13行と、`src`、`third_party/nlohmann` の行ができ、`third_party/nlohmann` のライセンスが `MIT AND Apache-2.0`、バージョンの初期値が `3.11.3` になること
2. 1. の台帳で何も記入せずに変換すると、区分が空の行を理由に止まること
3. `src` を対象外に、`third_party/nlohmann` を同梱にして記入すると変換でき、`testdata/linux-cmake/downstream/product.cdx.json` と部品の一覧が同じになり、`cyclonedx validate` に合格すること
4. 同じ部品名・バージョンの2行（`foo/include` と `foo/src`）のライセンスが合成されること
5. `1.10` というバージョンが Excel で開いて保存したあとも `1.10` のままで、SBOM にもそのまま入ること
6. 親子のフォルダの行のまとめ方：ライセンスと著作権者が一致すればまとまること、片方が未検出ならまとまらないこと
7. 出力先がすでにあると上書きせず止まること
8. Windows（`samples` 相当の msbuild ログ）で、同梱 DLL の部品名とバージョンに初期値が入り、MSVC と Windows SDK の行ができること
9. 実際の Excel で、作った台帳が修復メッセージなしで開け、プルダウンと色付けが動くこと。Excel で保存した台帳を `cpp_sbom convert` が読めること（保存したファイルはテストデータとして残す）
10. `go test ./...` と、Python がない環境での `cpp_sbom` の実行が通ること
11. `cpp_sbom check` が、未記入の台帳で終了コード 1 になって理由を出し、完成した台帳で終了コード 0 になること。実際の Excel で保存した台帳で、変換と判定が一致すること

12. Linux のビルドPCの結果（`deps.json`、`build-env.cdx.json`、`source.tar.gz`）を Windows に運び、ScanCode、`cpp_sbom` の3つのサブコマンド、`cyclonedx validate`、Grype、cve-bin-tool まで通ること。`internal/makeledger/crossos_test.go` が `testdata/linux-to-windows` の実データで台帳の内容を確かめる

### 実施結果（2026-11-07）

| 環境 | 確認した内容 |
| --- | --- |
| Ubuntu 24.04（コンテナ。Python なしの実行を含まない） | `test/acceptance_ubuntu.sh`：32項目すべて成功（`check` の3項目を含む）。ScanCode 32.5.0 の実物でプロジェクト全体を調査（サンプルで約40秒） |
| Rocky Linux 9（コンテナ） | `test/acceptance_rpm.sh`：`cpp_deps` がすべて成功 |
| 改名後の再確認（2026-11-07） | `acceptance_ubuntu.sh` は ScanCode と Syft を指定せず OK 30、NG 0。`acceptance_rpm.sh` はすべて成功 |
| Windows 11 と Excel 実機 | 台帳が修復なしで開ける（修復ログ0件）。プルダウン、文字列の書式、色の付き方と消え方を26の状態で確認。保存した台帳（`internal/tocdx/testdata/excel_saved.xlsx`）から変換 |
| Windows 11 と MSVC 実機 | msbuild のログから `cpp_deps` と `ledger` まで。MSVC と Windows SDK の版を自動で取得 |
| CycloneDX CLI | `cyclonedx validate` に合格。部品の一覧は検証時の SBOM と同じ（追加した検証用の1行と、apt の更新による `linux-libc-dev` の版の違いを除く） |

Linux → Windows（テスト 12。2026-11-07）：

| 作業 | 環境 | 結果 |
| --- | --- | --- |
| 作業1〜2、持ち出し | Ubuntu 24.04（コンテナ）。Syft は tar.gz を展開して実行 | CMake は OSパッケージ 13 個、項目 2 件。Make は 8 個、2 件。Syft は出力ファイル以外を残さない |
| 展開 | Windows 11 の `tar` | シンボリックリンクは `tar -h` で実体にする。リンク先のないものは入らない |
| 作業3 | ScanCode 32.5.0（py3.12、Windows） | サンプルで約40秒。パスが長いと初回設定に失敗（`C:\sbom-tools` 直下なら問題なし） |
| 作業4 | cve-bin-tool 3.4（Windows。`file` コマンドなし） | 静的リンクの OpenSSL 3.0.13 を `--sbom-output` で検出。zlib 1.3 はツールのパターンに合わず検出しない。NVD のデータは取得できない |
| 作業5 | `cpp_sbom-windows-amd64.exe`、CycloneDX CLI 0.30.0 | CMake の台帳15行、`third_party/nlohmann` は `MIT AND Apache-2.0`、`3.11.3`。openpyxl で記入して `check`、`convert`、`validate` に合格。Make は台帳の作成まで |
| 作業6 | Grype 0.121.2（Windows） | 30 件検出（PURL）。Linux 側の表示との比較は未実施。別に zlib 1.2.11 の SBOM で CPE の照合も確認。0.119.0 は `db import` が 9 回中 8 回失敗 |

受け入れテスト 1〜9 と 11 を上記で確認した（11 の完成した台帳は、Excel で保存した台帳を Windows のバイナリと `go test` で確認）。10 のうち `go test` は Windows と Linux で成功。Python がない環境での `cpp_sbom` は未確認（Python を呼ばない）。

作業4の結果との照合（2026-11-07）：cve-bin-tool 3.4 の実際の出力（`testdata/linux-to-windows/cvebin-parts.json`。パスだけ置き換え）と Linux → Windows の実データの台帳で、`openssl 3.0.13` が `libssl-dev 3.0.13-0ubuntu3.16` の行に結び付くことを確認。部品がない結果と、別の SBOM を指定した場合も実物で確認。RPM の PURL は単体テストの文字列だけで確認。Windows 向けの製品の台帳では未確認。

## 未検証・未決

- **ScanCode の所要時間とフォルダの行数：** 実製品で確認する。行が多すぎれば、まとめ方を見直す
- **ライセンスの実物確認：** 上の Ubuntu の結果の `--osenv` は `testdata` の Syft の結果を使った。Syft の出力の形は変更前と同じ
- **`scancode` にフォルダを `.` で渡したときの `path`：** 先頭要素の扱いが同じか、実装時に確認する。手順書ではフォルダのパスを指定する
- **バイナリの配布方法：** 未決。作業全体が2つのバイナリに依存するので、手順書を更新するまでに決める
- **Syft を `cpp_deps` に統合できるか：** 検討の結果、統合しない。Syft はインストールせず tar.gz を展開して使えば、ビルドPCに何も残らないため（2026-11-07 にコンテナで確認）
- **コンパイラ・標準ライブラリの扱い（CLAUDE.md の課題5）：** 未決のまま。決まれば「対象外」の初期値で実現できる
