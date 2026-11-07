# 自作ツールの配布物

`tools/cpp_deps/build.sh` は `cpp_deps` と `cpp_sbom` を Linux amd64・Linux arm64・Windows amd64 向けにビルドし、各バイナリの CycloneDX JSON SBOM、`THIRD_PARTY_LICENSES.txt`、`SHA256SUMS` を `dist/` に作る。ビルドPCには Go、Syft、`sha256sum` が必要。Syft の場所は `SYFT` 環境変数で指定できる。

各SBOMのファイル名は、対応するバイナリの末尾の `.exe` を除いて `.cdx.json` を付けたもの。SBOMはバイナリを直接調べ、組み込まれたGoモジュール、Go標準ライブラリ、対象ファイルのハッシュを記録する。バイナリから推測した製品CPEは含めない。Goモジュールのライセンス種別はSBOMには自動記入されないため、対応するライセンス本文を `THIRD_PARTY_LICENSES.txt` に収録する。

配布時には、対象OSのバイナリとそのSBOM、`THIRD_PARTY_LICENSES.txt`、`SHA256SUMS` を組にして渡す。このSBOMは**ツール自身の構成**であり、`cpp_sbom convert` が作る製品SBOMとは別物。

## リリース前の未決事項

- リポジトリのルートには、自作部分の利用・再配布条件を示す `LICENSE` がない。公開配布する場合は権利者が条件を決め、表示を追加する。
- リリース番号と配布バイナリの署名・`SHA256SUMS` の公開方法を確定する。現在のSBOMの自作モジュールの版はGoビルド情報から得たコミット由来の版で、正式なリリース番号ではない。
- リリース用コミットまたはタグを確定した後、作業ツリーが変更されていない状態で再ビルドし、SBOMとライセンス文を同時に作り直す。
