# 自作ツールの配布物

`tools/cpp_deps/build.sh` は `cpp_deps` と `cpp_sbom` を Linux amd64・Linux arm64・Windows amd64 向けにビルドし、各バイナリの CycloneDX JSON SBOM、`THIRD_PARTY_LICENSES.txt`、`SHA256SUMS` を `dist/` に作る。ビルドPCには Go、Syft、`sha256sum` が必要。Syft の場所は `SYFT` 環境変数、リリース番号は `RELEASE_VERSION` 環境変数で指定する。各バイナリの `--version` で番号を確認できる。

各SBOMのファイル名は、対応するバイナリの末尾の `.exe` を除いて `.cdx.json` を付けたもの。SBOMはバイナリを直接調べ、組み込まれたGoモジュール、Go標準ライブラリ、対象ファイルのハッシュを記録する。バイナリから推測した製品CPEは含めない。Goモジュールのライセンス種別はSBOMには自動記入されないため、対応するライセンス本文を `THIRD_PARTY_LICENSES.txt` に収録する。

配布時には、対象OSのバイナリとそのSBOM、`THIRD_PARTY_LICENSES.txt`、`SHA256SUMS` を組にして渡す。このSBOMは**ツール自身の構成**であり、`cpp_sbom convert` が作る製品SBOMとは別物。

`package_release.py v0.1.0` は、クリーンなコミットからビルドしたバイナリ、対応するSBOM、手順書PDF、ライセンス文をOS別のZIPにまとめる。

## リリース確認事項

- 自作部分の利用・再配布条件はルートの `LICENSE`（MIT）を参照する。
- 配布バイナリの署名は未実施。`SHA256SUMS` を各ZIPに同梱する。
- リリース用コミットまたはタグを確定した後、作業ツリーが変更されていない状態で再ビルドし、SBOMとライセンス文を同時に作り直す。
