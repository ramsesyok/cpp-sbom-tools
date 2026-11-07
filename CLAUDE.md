# C/C++ SBOM・脆弱性確認ツールキット

製品種別を「ソフトウェア製品」と「システム製品」に分けた現行手順は `docs/procedure/`、入力とSBOM範囲の仕様は `docs/product_scope_spec.md`。以下の検証履歴と旧方針には変更前の記述が含まれる。

既存の C/C++ プログラム（conan / vcpkg 非対応）について、SBOM（CycloneDX）を作り、脆弱性を確認するための手順とツール一式。
claude.ai での検討（2026-10-31〜11-07）から Claude Code に引き継いだもの。

## 前提・制約（変えないこと）

- **ソースコードはどんな形でも社外に出さない。** ハッシュ・フィンガープリントの送信も不可（SCANOSS・OSV-Scanner の C/C++ 検出などは使わない）
- **エアギャップ前提。** ネット接続PCはツールと脆弱性DBのダウンロード専用。ビルドPCと作業PCは外部通信なし
- **ビルドPCの環境はきれいに保つ。** インストールするのは bear だけ（Linux で Make の場合）。cpp_deps と Syft は置くだけで動かす。それ以降は別の Windows PC（作業PC）で行う
- **無償の OSS ツールのみ**（Syft、Grype、ScanCode Toolkit、CycloneDX CLI、cve-bin-tool、bear）
- **コンテナは対象外**（製品・ビルド環境ともに。ツール開発時のテスト用途は可）
- **手順書の読者は品質保証チーム**（ソフトウェア知識が高くない人）。説明は平易な日本語で、手作業の判断をなるべく残さない。人が判断する箇所は「要確認」として明示し、記載対象を標準作業にできる形にする
- 対象 OS は Linux（Ubuntu/Debian 系、Red Hat 系）と Windows（MSVC のみ。msbuild、CMake の Visual Studio ジェネレータ、nmake。MinGW / clang は対象外で、ツールが検出して注意を出す）。OS で分かれるのはビルドPCの作業だけで、作業PCは製品の OS によらず Windows

## 全体の流れ

| 作業 | 内容 | 実行場所 | ツール |
| --- | --- | --- | --- |
| 作業1 | ビルドで使ったヘッダ・ライブラリを記録し、OSパッケージへ逆引き | **ビルドPC** | bear / CMake、`-Wl,--trace`、`cpp_deps`（Windows: `_CL_=/showIncludes`、`_LINK_=/VERBOSE`） |
| 作業2 | ビルド環境のOSパッケージを記録（製品SBOMにはマージしない。ライセンス取得に使う）。Windows は作業1で自動分類 | **ビルドPC**（Linuxのみ） | Syft（dpkg/rpm カタログのみ。tar.gz を展開して使う） |
| 持ち出し | `deps.json`、`build-env.cdx.json`（Linuxのみ）、ビルドした状態のプロジェクト全体（`source.tar.gz`）を運ぶ | ビルドPC → 作業PC | tar |
| 作業3 | 同梱ソース（third_party 等）のライセンス・著作権・バージョン候補 | **作業PC（Windows）** | ScanCode |
| 作業4 | 成果物バイナリで部品の裏取り（脆弱性は見ない） | 作業PC | cve-bin-tool `--offline --sbom-output` |
| 作業5 | 台帳を作る → 色の付いたセルを人が記入 → 完成の確認 → CycloneDX 1.5 に変換 → 形式チェック | 作業PC | `cpp_sbom ledger` → `cpp_sbom check` → `cpp_sbom convert` → cyclonedx validate |
| 作業6 | 脆弱性照合（CPE と PURL） | 作業PC | Grype 0.121.2 以上（オフラインDB） |

## 主な決定事項と理由

- **照合は Grype。Trivy は不採用。** Trivy は CPE で照合しないため、台帳の「同梱」「別途導入」部品（CPE か pkg:generic）を検出できず、黙って0件になる。また SBOM に operating-system コンポーネントがないと OS パッケージも照合しない
- **Syft は作業2（ビルド環境の記録とライセンス取得）だけに使う。** OS 全体（検証環境で約1,000パッケージ）を製品 SBOM に入れるとノイズになるため。インストールせず、設定は環境変数で渡す（ビルドPCに何も残らないことをコンテナで確認）
- **Grype は Windows では 0.121.2 以上。** 0.119.0 は `db import` が「being used by another process」で高い頻度で失敗する（Defender 有効の Windows 11 で 9 回中 8 回失敗。0.121.2 は 3 回とも成功）
- **静的ライブラリはリンカのトレースで拾う**（Linux `-Wl,--trace`、Windows `/VERBOSE` の "Searching … / Found"）。ldd には静的リンクが出ないため
- **自作ツールは Go の2つのバイナリ（実行時に何も要らない）。** `cpp_deps`（ビルドPC用。収集だけで、外部ライブラリなし）と `cpp_sbom`（作業PC用。`ledger`、`check`、`convert`。Windows 版が手順書の対象で、Linux 版も配布物に含める）。外部ライブラリは Excel 用の `excelize` のみで `cpp_sbom` だけが使う（Excel との互換性を自前で持たないため）。製品の台帳と SBOM には載らない。ビルドはネット接続PCで行い、配布物にライセンス文を添える。Windows 版も dumpbin を使わず PE を直接読む。英語の言語パックがない VS では `VSLANG=1033` が効かないので、リンカの日本語出力も読む
- **台帳の全行を処理する方式。** 「やること」列の指示は数種類に固定。自社開発のもの（OSSを含まない）は区分を「対象外」にする。OSS を自社でビルドしたものは同梱扱い
- **台帳に必須の項目が空の行が残っていると、SBOM へ変換しない**
- **ScanCode のライセンス検出結果はそのまま使う**（例：nlohmann/json.hpp は Abseil 由来コードを含むため「MIT AND Apache-2.0」）
- **SBOM に operating-system コンポーネントを自動付与**（OSパッケージの PURL の `distro=` から）
- **cve-bin-tool は台帳の前（作業4）に実行し、作業5で台帳と照らし合わせる。** 漏れが SBOM を作る前に分かり、定期作業の照合（作業6）にも関わらないため
- **cve-bin-tool の結果は SBOM にマージせず、`cpp_sbom check --cvebin` で台帳と照合する。** マージすると OS パッケージと二重に載り、未確認の部品が必須項目のチェックを通らずに入るため。誤検出は「対象外」の行で記録する
- **cve-bin-tool は部品の検出だけに使い、脆弱性は Grype に一本化する。** 3.4 は NVD を取得できない（2026-11-07 時点。json-mirror と json-nvd は黙って0件、api2 はエラーで停止）ので、CSV の CVE は当てにならない。`--sbom-output` なら CVE がなくても検出した部品が出る
- **cve-bin-tool のデータ取得は `-n json-nvd -d "OSV,GAD"`。** OSV は Windows にない gsutil を呼んで落ち、GAD は不正な YAML を含むデータで落ちる。`--import` の前に `~/.cache/cve-bin-tool` を作る

## 現在の課題（次にやること）

1. **構成を変更した（2026-11-07）。** ビルドPCは作業2まで、作業3以降は別の Windows PC（作業PC）で行う形にし、ツールを `cpp_deps`（収集のみ）と `cpp_sbom`（台帳・check・SBOM変換）に分け直した。手順書は `docs/procedure/` を先に書き換えた
   - **正本（claude.ai）へ反映済み（2026-11-07）。** タブは 0〜6 の7つ（はじめに・付録、ビルドPC Linux、ビルドPC Windows、作業PCの準備、SBOM作成、脆弱性チェック、定期作業）。以後の変更も `docs/procedure/` を先に直し、レビュー後に正本へ反映する
   - 未確認：ScanCode の所要時間とフォルダの行数は実製品で確認する
   - バイナリの配布方法（手順書では「本書の管理者から受け取る」）は未決
   - 仕様は `docs/cpp_deps_spec.md` と `docs/ledger_spec.md`。運用前なので互換性は不要
2. Linux → Windows は Ubuntu 24.04 コンテナと Windows 11 で作業1〜6を実データで確認した（CMake のサンプル。結果の例は `testdata/linux-to-windows/`、詳細は `docs/ledger_spec.md`）。作業6は CPE と PURL の両方で検出を確認。作業4は静的リンクの OpenSSL で確認
3. Windows 側は手順書どおりの msbuild のログまで確認済み（Visual Studio 18、日本語の言語パックのみ）。CMake（Visual Studio ジェネレータ）と nmake は小さなサンプルでログの読み取りまで確認。結果は `docs/cpp_deps_spec.md`。英語のリンカ出力は実機で未確認
4. nlohmann-json の CPE（`nlohmann:json`）は仮。`grype db search --pkg nlohmann` は0件で、DB に登録がなく確かめられない
5. 方針未決：コンパイラ・標準ライブラリ（libgcc、libstdc++、glibc、linux-libc-dev）を製品 SBOM にどう載せるか。配布形態（ソフトのみ配布 / OS ごと配布）で決める案。あわせて -dev と実行時パッケージをソースパッケージ単位で1行にまとめる案（`cpp_sbom ledger` の改修）
6. OSパッケージのライセンスは Debian copyright の全ライセンスを AND で並べたもの（広めに出る）。運用ルールは未決

## フォルダ構成

- `docs/procedure/` … 手順書のスナップショット（章は ビルドPC Linux / Windows、作業PC 準備 / SBOM / 脆弱性、定期作業、付録）。標準の Quarto Book で、`docs/procedure` で `quarto render` を実行すると `_book/` に HTML と PDF（`sbom-procedure.pdf`）ができる。PDF は Typst で作るので LaTeX は不要。Windows のフォント（BIZ UD ゴシック、Consolas）を指定している。正本は claude.ai のドキュメント https://claude.ai/artifact/56722PZX6RPvYa8f1wbfHc
- `docs/cpp_deps_spec.md` … `cpp_deps`（旧 collect_cpp_deps）の仕様（Linux・Windows）
- `docs/ledger_spec.md` … Excel の台帳と `cpp_sbom` の仕様、受け入れテストの結果
- `tools/cpp_deps/` … 2つのバイナリの Go 実装。`sh build.sh` で `dist/` にバイナリとライセンス文を作る。`test/` に受け入れテスト。台帳の列と必須の項目の定義は `internal/ledger/` にだけ書く
- `samples/` … 検証用サンプル。paho.mqtt.c 1.3.13 / paho.mqtt.cpp 1.2.0（Ubuntu 24.04 の apt）＋ nlohmann-json 3.11.3（third_party に単一ヘッダを同梱）
  - `mqtt-client-cmake`：CMake、動的リンク
  - `mqtt-client-make`：Make＋bear、paho.mqtt.c を静的リンク（`-Wl,-Bstatic -lpaho-mqtt3c`）
- `testdata/` … 検証時の入力と出力。**git には含めない**（`.gitignore`。取得した PC のパスが入るため）。ないときは、使う `go test` は飛ばし、`acceptance_ubuntu.sh` は `SYFT` の指定が必要（Ubuntu 24.04 上で取得。パスとパッケージ版は取得環境に依存）。`linux-to-windows/` は Linux の結果を Windows で処理した検証の入力で、`go test` が使う

## 検証環境の再現（Ubuntu 24.04）

```bash
sudo apt install build-essential cmake bear libpaho-mqtt-dev libpaho-mqttpp-dev mosquitto
cd samples/mqtt-client-cmake
cmake -S . -B build -DCMAKE_EXPORT_COMPILE_COMMANDS=ON \
  -DCMAKE_EXE_LINKER_FLAGS="-Wl,--trace" -DCMAKE_SHARED_LINKER_FLAGS="-Wl,--trace"
cmake --build build --clean-first 2>&1 | tee build_link.log
../../tools/cpp_deps/dist/cpp_deps-linux-amd64 --compdb build/compile_commands.json \
  --linklog build_link.log --bin build --src . --out /tmp/deps.json
```

期待結果：OSパッケージ13個（paho C/C++ の dev と実行時、libssl-dev / libssl3t64、libc6 系、libgcc 系、libstdc++ 系、linux-libc-dev）、人の判断が必要なもの2件（`src`、`third_party/nlohmann` のヘッダフォルダ）。

## 書き方のルール

- 手順書・メッセージ・CSV の列名は日本語。CSV は Excel で開けるよう UTF-8（BOM付き）で出力する
- 自作ツールは外部通信をしない
- 手順書の本文から他の章へはリンクせず、章番号で書く（Quarto の Typst 出力では `.qmd` へのリンクが1行に分かれ、リンク先にも飛べない）
- PowerShell スクリプトは BOM 付き UTF-8 で保存する（Windows PowerShell 5.1 対策）
