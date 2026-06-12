# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-06-12

### Added

- **ライフサイクル実行エンジン** (`internal/lifecycle`): これまで CRUD API で受理するだけだったバケットライフサイクル設定を、実際に実行するバックグラウンドエンジンを実装。サーバー内蔵の `time.Ticker`（既定 1h、起動 1 分後に初回）で周期実行し、`jog lifecycle run [--bucket B] [--dry-run]` サブコマンドで手動実行もできる。
  - 対応アクション: Expiration（Enabled バケットは delete marker 生成・データ非破壊／非バージョニングは物理削除）、NoncurrentVersionExpiration（`NoncurrentDays` / `NewerNoncurrentVersions`、delete marker は対象外）、ExpiredObjectDeleteMarker、AbortIncompleteMultipartUpload。Transition は単一ノードでは no-op。
  - **最上位の安全性保証**: COMPLIANCE/GOVERNANCE 保持期限内・legal hold ON のバージョンは、いかなる経路・中断状態でも削除しない（fail-closed）。削除は新設の `BEGIN IMMEDIATE` トランザクション内ガード経由のみで、ガードの読み取りと削除が同一書き込みロック下に置かれる。GOVERNANCE もエンジンは bypass しない。
  - クラッシュ安全性: 「行削除→commit→ファイル unlink」順により、中断は孤立ファイル（不可視・GC 可能）しか残さない。孤立ファイルは猶予付き GC が回収する。
  - `JOG_LIFECYCLE_ENABLED`（既定 true）/ `JOG_LIFECYCLE_INTERVAL`（既定 1h）/ `JOG_LIFECYCLE_MAX_ACTIONS`（既定 10000）で設定可能。
- **バケット通知設定** (`PutBucketNotificationConfiguration` / `GetBucketNotificationConfiguration`): 通知設定の保存・取得 API を実装（設定の CRUD のみ。イベントの実配信は未対応）(#31, #32)。
- **per-version Object Lock**: Object Lock の retention / legal hold をバージョン単位で保持するスキーマと起動時マイグレーションを追加 (#39)。書き込み時に `x-amz-object-lock-*` ヘッダおよびバケットの DefaultRetention を該当バージョンへ適用する (#40)。
- **per-version ACL / タグ**: `object_acls` / `object_tags` をバージョン単位に拡張し、起動時マイグレーションで既存行を移行 (#41)。
- `internal/objectlock`: Object Lock 評価ロジック (`EvaluateDeletable`) を HTTP 非依存のパッケージへ抽出（API とライフサイクルエンジンで共用）。
- `internal/storage`: ガード付きトランザクショナル削除 primitive（`withImmediateTx` ほか）と、ライフサイクル走査用のキー列挙・バージョン取得・観測用 `lifecycle_runs` テーブルを追加。

### Fixed

- `GetLatestObjectVersion`: 同一 `last_modified` の複数バージョンで latest 判定が非決定的になる問題を `version_id DESC` タイブレークで修正（current を noncurrent と誤判定して消しすぎる経路を防ぐ）。
- **ライフサイクル削除と並行 PUT の TOCTOU（current ファイルの誤削除）を修正**: エンジンの current 削除（`CreateExpirationDeleteMarker`/`ExpireCurrentObjectGuarded`）は commit 後に `dataDir/bucket/key` を unlink するが、その間に同一キーへの PUT が完了すると成功した書き込みのファイルを消しうる（非バージョニングはデータ損失）。`FileSystem` に (bucket,key) 単位の striped mutex を追加し、PUT 経路とエンジンの current 削除を直列化（ロック順 keyMu→SQLite でデッドロック無し、遅い body コピーはロック外）。`-race` の並行回帰テストで固定。
- **ライフサイクルのキー列挙 `ListLifecycleObjectKeys` のページングが O(n²) だった問題を修正**: `objects UNION object_versions` の各ブランチに `LIMIT` が押し下がっておらず、ページごとに残り全キーを走査していた。各ブランチに `ORDER BY key LIMIT`（object_versions は `DISTINCT key`）を押し下げて線形化。100 万キーのスキャンが約 384s → 約 21.6s（約 18 倍）。version-only キー（current が delete marker）でも漏れないことをテストで固定。
- **SQLite DSN の WAL / busy_timeout が無効だった問題を修正**: 接続文字列が mattn/go-sqlite3 形式の `_journal_mode=WAL` / `_busy_timeout=5000` を使っており、modernc.org/sqlite ではこれらが黙って無視されていた（実測で `journal_mode=delete`・`busy_timeout=0`）。正しい `_pragma=journal_mode(WAL)` / `_pragma=busy_timeout(5000)` 形式に修正。これにより (1) 読み取りが書き込みをブロックしなくなり並行性が改善、(2) 書き込み競合が即 `SQLITE_BUSY` 失敗せず待機するようになり、(3) `docs/DEPLOYMENT.md` の Litestream レプリケーション（WAL 必須）が実際に機能する。ライフサイクルエンジンの `withImmediateTx` はエンジン用コネクションにのみ短い busy_timeout(100ms) を設定し、競合時に速やかに skip（fail-closed）する挙動を維持。ベンチ実測でエンジン競合下の Put レイテンシ悪化が約4.6倍→約1.13倍に改善（BUSY リトライ 13.5/op → 0/op）。
- `DeleteBucket`: 空判定で `objects` だけでなく `object_versions` とマルチパートアップロードもカウントするよう修正（非カレントバージョンや未完了アップロードが残るバケットを誤って削除できた問題）(#42)。
- object / version の削除時に `object_acls` / `object_tags` の孤立行を掃除するよう修正。非バージョニングの `DeleteObject` 経路も対象に含めた (#47)。
- `GetBucketVersioning` がエラーを返した場合に、書き込み・削除経路で fail-closed にするよう修正（バージョニング状態が判定できないときは処理を継続せず安全側へ倒す）(#48)。

### Security

- **セキュリティ監査で検出された Critical / High の指摘に対応** (#36, #37, #38):
  - Object Lock を `DELETE` / `PUT` 上書き / `DeleteObjects` / `CopyObject` 上書き / `PutObjectRetention` / マルチパート完了経路で enforce し、保護対象は fail-closed で拒否（バージョニング有無による carve-out も解消）。
  - SigV4 署名比較を定数時間化し、長さ不一致によるタイミングリークを解消。
  - `PutObject` / `UploadPart` の `Content-Length` 上限を検証。
  - `CopyObject` の metadata-directive と自己コピーを検証。
  - 静的ウェブサイトのリダイレクト `Protocol` / `HostName` / `HttpRedirectCode` を検証。
  - Docker コンテナを非 root ユーザーで実行。
  - `--secret-key` を `--secret-key-file` に置き換え、秘密鍵がプロセス引数に露出しないよう修正。
  - CORS プリフライト (`OPTIONS`) リクエストを認証必須から除外。

### 互換性

- `object_versions` 上の Object Lock / ACL / タグの per-version 化（#39 / #41）は起動時マイグレーションで既存行を自動移行する後方互換変更で、ユーザー操作は不要（移行は冪等で、混在世代スキーマは拒否してデータ不整合を防ぐ）。
- 上記以外に既存テーブルの破壊的スキーマ変更はなし（`lifecycle_runs` は additive、`PRAGMA user_version` の繰り上げなし）。Versioning の既存挙動・テストは不変。

## [0.1.3] - 2026-05-07

### Changed

- `internal/config`: `github.com/spf13/viper` への依存を除去し、`gopkg.in/yaml.v3` + 自前の環境変数オーバーレイ実装に置き換え。サプライチェーン上の懸念がある `github.com/fsnotify/fsnotify` ほか viper 推移依存（afero, hcl, mapstructure, sagikazarmark/*, sourcegraph/conc, subosito/gotenv, magiconair/properties, pelletier/go-toml/v2 など）が `go.sum` から除去された (#28, #29)

### Added

- `internal/config/config_test.go`: デフォルト値・環境変数オーバーライド・YAML ファイル探索パスの優先順位・空 env の扱いなどをカバーするユニットテスト (15 ケース)

### 互換性

- 設定ファイル (`config.yaml`) のスキーマは変更なし（`mapstructure` タグ → `yaml` タグの移行のみ。snake_case キーはそのまま）
- 環境変数の名前・優先順位（env > file > default）も従来どおり

## [0.1.0] - 2026-01-23

### Added

- S3 compatible API server with core operations
- Bucket operations: CreateBucket, DeleteBucket, ListBuckets, HeadBucket, GetBucketLocation
- Object operations: PutObject, GetObject, DeleteObject, HeadObject, CopyObject
- ListObjects (v1/v2) with prefix, delimiter, and pagination support
- Multipart upload support (CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload, ListParts, ListMultipartUploads)
- AWS Signature V4 authentication
- Bucket tagging (PutBucketTagging, GetBucketTagging, DeleteBucketTagging)
- Object tagging (PutObjectTagging, GetObjectTagging, DeleteObjectTagging)
- CORS configuration (PutBucketCors, GetBucketCors, DeleteBucketCors)
- Bucket versioning (PutBucketVersioning, GetBucketVersioning)
- Object versions (GetObject with versionId, DeleteObject with versionId, ListObjectVersions)
- ACL support (PutBucketAcl, GetBucketAcl, PutObjectAcl, GetObjectAcl)
- Bucket encryption (PutBucketEncryption, GetBucketEncryption, DeleteBucketEncryption)
- Bucket lifecycle (PutBucketLifecycleConfiguration, GetBucketLifecycleConfiguration, DeleteBucketLifecycleConfiguration)
- Object Lock (PutObjectLockConfiguration, GetObjectLockConfiguration, PutObjectRetention, GetObjectRetention, PutObjectLegalHold, GetObjectLegalHold)
- Bucket policy (PutBucketPolicy, GetBucketPolicy, DeleteBucketPolicy)
- Static website hosting (PutBucketWebsite, GetBucketWebsite, DeleteBucketWebsite)
- DeleteObjects (bulk delete)
- AWS Chunked encoding (streaming payload signature) support
- SQLite-based metadata storage with WAL mode
- Docker and Docker Compose support
- Comprehensive S3 compatibility test suite using AWS SDK for Go v2
- Path traversal attack protection for object keys

[unreleased]: https://github.com/kumasuke/jog/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/kumasuke/jog/compare/v0.1.3...v0.2.0
[0.1.3]: https://github.com/kumasuke/jog/releases/tag/v0.1.3
[0.1.0]: https://github.com/kumasuke/jog/releases/tag/v0.1.0
