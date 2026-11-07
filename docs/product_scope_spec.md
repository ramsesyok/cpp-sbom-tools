# 製品種別によるSBOM範囲（現行仕様）

2026-11-07。操作手順は `docs/procedure/` を正とする。

| 製品種別 | `cpp_sbom ledger` | OSの情報源 | 製品SBOM |
| --- | --- | --- | --- |
| ソフトウェア製品 | `--product software --delivery <納入ファイル>` | ビルド環境は別記録 | 納入物に取り込んだ部品、同梱した部品。ビルドPCのOSは追加しない |
| システム製品 | `--product system --delivery <納入ファイル> --system-inventory <納入構成のCycloneDX>` | 完成した納入構成 | 左記に加え、納入OSとOSパッケージ・導入済みソフト |

`--product` と `--delivery` は必須。Linuxの `deps.json` には引き続き `--osenv build-env.cdx.json` が必要だが、これはビルド環境のライセンスを引くためだけに使う。納入OSの入力とは別。納入形態はExcel台帳の「作成条件」シートに保存し、`check` と `convert` が確認する。

`cpp_sbom ledger` は `deps.json` のOSパッケージを納入物のファイル名と突き合わせる。これは版やファイル内容の一致までは証明しない。静的ライブラリの所属パッケージは「同梱」候補、ヘッダだけのパッケージは人の確認行とする。納入物にない動的ライブラリとビルド用MSVC/Windows SDKは製品台帳に入れない。元の検出記録は「調査結果」に残す。納入物内のDLLは、PEのimport表に現れないものも候補にする。

システム製品の `--system-inventory` はCycloneDX JSON。Linuxのdpkg/rpmパッケージはPURLの `distro=` から納入OSを導く。Windowsは `cpp_deps inventory` がOSと機器全体の登録アプリを記録し、更新プログラムは `Win32_QuickFixEngineering` が見える範囲をCSVに分ける。アプリのPublisherは参考列に残す。OSが不明な入力、複数のLinuxディストリビューションが混在する入力は拒否する。台帳に納入OSがちょうど1行必要。

OSパッケージのライセンスは、調査結果にない場合は空欄を許す。導入済みソフトの版とCPE/PURLは必須で、CPEが見つからない場合は「なし」と明記する。これでも脆弱性の網羅を保証しない。WindowsのOS更新、ユーザー別アプリ、ポータブルアプリ、ドライバ・ファームウェアは別の納入構成記録とベンダーの修正情報で確認する。

旧仕様と受け入れ試験の履歴は `docs/ledger_spec.md` に残す。旧台帳に「作成条件」シートがない場合は範囲が曖昧なので `check` と `convert` は拒否する。製品種別を指定して台帳を作り直す。
