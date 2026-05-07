# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[unreleased]: https://github.com/kumasuke/jog/compare/v0.1.3...HEAD
[0.1.3]: https://github.com/kumasuke/jog/releases/tag/v0.1.3
[0.1.0]: https://github.com/kumasuke/jog/releases/tag/v0.1.0
