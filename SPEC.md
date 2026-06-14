# JOG - S3互換オブジェクトストレージサーバー

## 概要

JOG (Just Object Gateway) は、Go言語で実装されたS3互換のオブジェクトストレージサーバーです。MinIOやRustFSを参考に、シンプルかつ高性能な設計を目指します。

## 設計目標

- **S3 API互換性**: AWS S3の主要APIと互換性を持つ
- **シンプルさ**: 単一バイナリで動作、依存関係を最小化
- **高性能**: Go言語の並行処理を活用した効率的なI/O
- **拡張性**: 将来的にエッジコンピューターやWebUIに対応可能な設計

## フェーズ別実装計画

### Phase 1: 基盤構築 (MVP)

#### 1.1 CLIフレームワーク
- [x] `jog server` - サーバー起動コマンド
- [ ] `jog config` - 設定管理
- [x] `jog version` - バージョン表示

#### 1.2 基本的なS3 API
**バケット操作**
- [x] `PUT /{bucket}` - CreateBucket
- [x] `DELETE /{bucket}` - DeleteBucket
- [x] `GET /` - ListBuckets
- [x] `HEAD /{bucket}` - HeadBucket

**オブジェクト操作**
- [x] `PUT /{bucket}/{key}` - PutObject
- [x] `GET /{bucket}/{key}` - GetObject
- [x] `DELETE /{bucket}/{key}` - DeleteObject
- [x] `GET /{bucket}?list-type=2` - ListObjectsV2
- [x] `HEAD /{bucket}/{key}` - HeadObject

#### 1.3 ストレージバックエンド
- [x] ローカルファイルシステムバックエンド
- [x] メタデータ管理 (SQLite)

#### 1.4 認証
- [x] Access Key / Secret Key認証 (AWS Signature V4)

#### 1.5 S3互換性テスト
- [x] AWS SDK for Go v2 を使った統合テスト
- [x] 全APIエンドポイントの互換性テスト
- [x] エラーレスポンス形式の互換性テスト

### Phase 2: 機能拡充

#### 2.1 マルチパートアップロード
- [x] CreateMultipartUpload
- [x] UploadPart
- [x] CompleteMultipartUpload
- [x] AbortMultipartUpload
- [x] ListParts
- [x] ListMultipartUploads

#### 2.2 追加オブジェクト操作
- [x] CopyObject
- [x] DeleteObjects (一括削除)
- [x] GetObjectAttributes

#### 2.3 バケットポリシー
- [x] PutBucketPolicy
- [x] GetBucketPolicy
- [x] DeleteBucketPolicy

#### 2.4 バージョニング
- [x] PutBucketVersioning
- [x] GetBucketVersioning
- [x] オブジェクトバージョン管理

#### 2.5 バケット通知
- [x] PutBucketNotificationConfiguration
- [x] GetBucketNotificationConfiguration
- [x] 通知イベント配信 (Webhook / HTTP POST) — ライフサイクル削除で `s3:LifecycleExpiration:Delete` / `s3:LifecycleExpiration:DeleteMarkerCreated` を発行（#56）
- [ ] 通知イベント配信 (SNS/SQS/Lambda/EventBridge)

### Phase 3: 運用機能

- [ ] アクセスログ
- [ ] メトリクス (Prometheus形式)
- [ ] ヘルスチェックエンドポイント
- [ ] TLS対応
- [x] CORS設定

### Phase 4: WebUI (将来)

- [ ] ダッシュボード
- [ ] バケット/オブジェクトブラウザ
- [ ] ユーザー管理画面

### Phase 5: エッジ対応 (将来)

- [ ] 軽量化ビルド
- [ ] ARM対応
- [ ] レプリケーション機能

### API制限・入力バリデーション

- **リクエストボディサイズ上限**（#34）: XML系サブリソース API（ACL / CORS / 暗号化 / ライフサイクル / 通知 / オブジェクトロック / タグ付け / バージョニング / Website）と一括削除（DeleteObjects）・マルチパート完了は、AWS S3 に倣ったボディサイズ上限を持つ。上限超過時は HTTP 413 `EntityTooLarge` を返す。オブジェクト本体は単一 PUT / UploadPart ともに 5 GiB が上限。
- **ライフサイクル EODM バリデーション**（#55）: `PutBucketLifecycleConfiguration` で `ExpiredObjectDeleteMarker` を `Expiration.Days` / `Expiration.Date` と同時指定したルールは HTTP 400 `InvalidRequest` で拒否する。
- **削除パスの輻輳**（#54）: 書き込み競合で削除トランザクションが失敗した場合、HTTP 503 `SlowDown` を返してクライアントにバックオフ再試行を促す。
- **SelectObjectContent**（S3 Select）: オブジェクトに対して SQL サブセットでクエリを実行し、合致した行だけを AWS EventStream バイナリ形式でストリーミング返却する。
  - **エンドポイント**: `POST /{bucket}/{key}?select&select-type=2`
  - **入力形式**: CSV（ヘッダー行あり/なし・任意の区切り文字）、JSON（Document / Lines）。JSON Document は単一 JSON オブジェクトを対象とし、配列ドキュメント（`[{...}, {...}]`）のイテレーションは未対応。**Parquet および gzip/bzip2 等の圧縮も未対応**（指定すると HTTP 400 `InvalidArgument`）
  - **SQL サブセット**:
    - 射影: `SELECT *` / 位置参照 `_1, _2, ...` / ヘッダー名 / テーブルエイリアス付き（例: `SELECT s.name FROM S3Object s`）
    - フィルタ: `WHERE` 句、比較演算子（`=` `!=` `<>` `<` `<=` `>` `>=`）、論理演算子（`AND` / `OR` / `NOT`）、括弧グルーピング
    - 制限: `LIMIT n`
    - 型比較: 両辺が数値として解釈可能なら数値比較、それ以外は文字列比較（弱い型付け）
    - **未対応**: 集約関数（`COUNT` / `SUM` / `AVG` / `MIN` / `MAX`）、SQL 組み込み関数、`CAST`（後続 PR で対応予定）
  - **出力形式**: `OutputSerialization` に従い CSV または JSON Lines を生成
  - **レスポンス**: AWS EventStream バイナリフレーム（`Records` イベント → `Stats` イベント → `End` イベント）
  - **エラーコード**: バケット不在 → `NoSuchBucket`（404）、オブジェクト不在 → `NoSuchKey`（404）、不正な XML リクエスト → `MalformedXML`（400）、未対応の入力形式/不正な SQL/オブジェクト本体が指定形式（CSV/JSON）としてパースできない → `InvalidArgument`（400）
  - **versionId 非対応**: S3 の SelectObjectContent API は `versionId` パラメータを持たず（AWS SDK の `SelectObjectContentInput` に `VersionId` フィールドが存在しない）、常に現行バージョンを対象とする。JOG もこれに倣う。
  - **実装**: `internal/api/select.go`（ハンドラ）、`internal/s3select/`（SQL パーサ・評価エンジン・CSV/JSON I/O）

---

## アーキテクチャ

```
┌─────────────────────────────────────────────────────────────┐
│                         CLI (cobra)                         │
├─────────────────────────────────────────────────────────────┤
│                     HTTP Server (net/http)                  │
├─────────────────────────────────────────────────────────────┤
│                      S3 API Handler                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │   Bucket    │  │   Object    │  │    Multipart        │  │
│  │   Handler   │  │   Handler   │  │    Handler          │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
├─────────────────────────────────────────────────────────────┤
│                    Auth Middleware                          │
│              (AWS Signature V4 Verification)                │
├─────────────────────────────────────────────────────────────┤
│                    Storage Layer                            │
│  ┌─────────────────────┐  ┌─────────────────────────────┐   │
│  │   Object Storage    │  │    Metadata Storage         │   │
│  │   (File System)     │  │    (SQLite)                 │   │
│  └─────────────────────┘  └─────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

## ディレクトリ構成

```
jog/
├── cmd/
│   └── jog/
│       └── main.go           # エントリポイント
├── internal/
│   ├── cli/                  # CLIコマンド定義
│   │   ├── root.go
│   │   ├── server.go
│   │   ├── config.go
│   │   └── version.go
│   ├── server/               # HTTPサーバー
│   │   ├── server.go
│   │   ├── router.go
│   │   └── middleware.go
│   ├── api/                  # S3 APIハンドラ
│   │   ├── bucket.go
│   │   ├── object.go
│   │   └── multipart.go
│   ├── auth/                 # 認証
│   │   └── signature_v4.go
│   ├── storage/              # ストレージ抽象化
│   │   ├── interface.go
│   │   ├── filesystem.go
│   │   ├── metadata.go
│   │   └── lifecycle_storage.go  # ライフサイクル用ガード付き削除 primitive
│   ├── objectlock/           # Object Lock 評価ロジック (HTTP非依存・共用)
│   │   └── evaluate.go
│   ├── lifecycle/            # ライフサイクル実行エンジン
│   │   ├── engine.go
│   │   ├── evaluate.go
│   │   └── actions.go
│   └── config/               # 設定管理
│       └── config.go
├── pkg/                      # 公開パッケージ (将来用)
├── test/
│   ├── s3compat/             # S3互換性テスト (AWS SDK使用)
│   │   ├── suite_test.go
│   │   ├── bucket_test.go
│   │   ├── object_test.go
│   │   └── error_test.go
│   └── testutil/             # テストユーティリティ
│       └── server.go
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile               # (将来追加予定)
├── README.md
└── SPEC.md
```

## 依存ライブラリ

| ライブラリ | 用途 |
|-----------|------|
| `github.com/spf13/cobra` | CLIフレームワーク |
| `gopkg.in/yaml.v3` | 設定管理 (YAML パース。環境変数バインドは `internal/config` で実装) |
| `modernc.org/sqlite` | メタデータDB (Pure Go) |
| `github.com/google/uuid` | UUID生成 |
| `github.com/rs/zerolog` | ロギング |
| `github.com/aws/aws-sdk-go-v2` | S3互換性テスト |
| `github.com/aws/aws-sdk-go-v2/service/s3` | S3 API テスト |
| `github.com/stretchr/testify` | テストアサーション |

## 設定

### 環境変数

| 変数名 | 説明 | デフォルト |
|--------|------|-----------|
| `JOG_SERVER_PORT` | リッスンポート | `9000` |
| `JOG_SERVER_ADDRESS` | リッスンアドレス | `0.0.0.0` |
| `JOG_STORAGE_DATA_DIR` | データ保存ディレクトリ | `./data` |
| `JOG_STORAGE_METADATA_DB` | メタデータDBパス | `./data/metadata.db` |
| `JOG_AUTH_ACCESS_KEY` | アクセスキー | `minioadmin` |
| `JOG_AUTH_SECRET_KEY` | シークレットキー | `minioadmin` |
| `JOG_LOGGING_LEVEL` | ログレベル | `info` |
| `JOG_LOGGING_FORMAT` | ログ形式 (`json` / `console`) | `json` |
| `JOG_LIFECYCLE_ENABLED` | ライフサイクル実行エンジンの有効化 | `true` |
| `JOG_LIFECYCLE_INTERVAL` | 実行周期 (Go duration) | `1h` |
| `JOG_LIFECYCLE_MAX_ACTIONS` | 1サイクルの最大アクション数 | `10000` |
| `JOG_NOTIFICATION_REGION` | 通知イベントの `awsRegion` に刻む値 | `us-east-1` |
| `JOG_NOTIFICATION_DELIVERY_TIMEOUT` | Webhook 1 配信のタイムアウト (Go duration) | `10s` |
| `JOG_NOTIFICATION_BLOCK_PRIVATE_TARGETS` | loopback/link-local/private/unspecified 宛の Webhook 配信を拒否 (SSRF対策) | `false` |

> 通知の配信先（ARN → Webhook URL のマッピング）は環境変数では設定できず、`config.yaml` の `notification.targets` でのみ指定する。

### 設定ファイル (config.yaml)

```yaml
server:
  port: 9000
  address: "0.0.0.0"

storage:
  data_dir: "./data"
  metadata_db: "./data/metadata.db"

auth:
  access_key: "minioadmin"
  secret_key: "minioadmin"

logging:
  level: "info"
  format: "json"

lifecycle:
  enabled: true              # ライフサイクル実行エンジン (JOG_LIFECYCLE_ENABLED)
  interval: 1h               # 実行周期 (JOG_LIFECYCLE_INTERVAL, Go duration)
  max_actions_per_cycle: 10000  # 1サイクルの最大アクション数 (JOG_LIFECYCLE_MAX_ACTIONS)

notification:
  region: "us-east-1"        # 通知イベントの awsRegion (JOG_NOTIFICATION_REGION)
  delivery_timeout: 10s      # Webhook 1 配信のタイムアウト (JOG_NOTIFICATION_DELIVERY_TIMEOUT)
  block_private_targets: false  # loopback/private 宛の配信を拒否 (JOG_NOTIFICATION_BLOCK_PRIVATE_TARGETS)
  # targets はバケット通知設定の ARN を実際の Webhook URL に対応付ける。
  # 1 つ以上設定されたときだけライフサイクル期限切れ通知が有効になる。
  targets:
    "arn:aws:sns:us-east-1:123456789012:lifecycle-events": "https://example.com/webhook"
```

> **Webhook配信のSSRF対策**: 配信用 HTTP クライアントはリダイレクト（3xx）を一切追従しない。外部 Webhook 受信側が `169.254.169.254`（メタデータ）や内部アドレスへ 302 誘導しても追従せず、非2xx として best-effort 失敗にマップされる。さらに `block_private_targets: true`（デフォルト false）で loopback/link-local/private/unspecified 宛の配信を接続時に拒否できる。`http`/`https` 以外のスキームの URL は常に拒否される。

## 使用例

### サーバー起動

```bash
# デフォルト設定で起動
jog server

# ポート指定
jog server --port 9000

# 設定ファイル指定
jog server --config /path/to/config.yaml
```

### AWS CLIでの操作

```bash
# エンドポイント設定
export AWS_ENDPOINT_URL=http://localhost:9000
export AWS_ACCESS_KEY_ID=minioadmin
export AWS_SECRET_ACCESS_KEY=minioadmin

# バケット作成
aws s3 mb s3://my-bucket

# ファイルアップロード
aws s3 cp file.txt s3://my-bucket/

# ファイル一覧
aws s3 ls s3://my-bucket/

# ファイルダウンロード
aws s3 cp s3://my-bucket/file.txt ./downloaded.txt
```

## 開発

### ビルド

```bash
# ビルド
make build

# テスト
make test

# リント
make lint
```

### テスト

```bash
# ユニットテスト
go test ./...

# E2Eテスト (サーバー起動後)
make e2e-test
```

## ライセンス

Apache License 2.0

---

## S3互換性テスト戦略

### テストの目的

AWS SDK for Go v2 を使用してJOGサーバーに対してテストを実行することで、S3互換性を担保する。
実際のAWS SDKがクライアントとして動作することで、APIの互換性を保証する。

### テスト構成

```
test/
├── s3compat/                    # S3互換性テスト
│   ├── suite_test.go           # テストスイート共通設定
│   ├── bucket_test.go          # バケット操作テスト
│   ├── object_test.go          # オブジェクト操作テスト
│   ├── multipart_test.go       # マルチパートアップロードテスト
│   └── error_test.go           # エラーレスポンステスト
└── testutil/
    └── server.go               # テスト用サーバー起動ヘルパー
```

### テストケース一覧

#### Phase 1 (MVP) テスト

**バケット操作**
| テストケース | 検証内容 |
|-------------|---------|
| `TestCreateBucket` | バケット作成が成功すること |
| `TestCreateBucketAlreadyExists` | 既存バケット作成時に適切なエラーが返ること |
| `TestCreateBucketInvalidName` | 無効なバケット名でエラーが返ること |
| `TestListBuckets` | バケット一覧が正しく返ること |
| `TestHeadBucket` | バケットの存在確認ができること |
| `TestHeadBucketNotFound` | 存在しないバケットで404が返ること |
| `TestDeleteBucket` | 空のバケットが削除できること |
| `TestDeleteBucketNotEmpty` | 空でないバケットの削除でエラーが返ること |

**オブジェクト操作**
| テストケース | 検証内容 |
|-------------|---------|
| `TestPutObject` | オブジェクトのアップロードが成功すること |
| `TestPutObjectWithMetadata` | カスタムメタデータ付きでアップロードできること |
| `TestGetObject` | オブジェクトのダウンロードが成功すること |
| `TestGetObjectNotFound` | 存在しないオブジェクトで404が返ること |
| `TestGetObjectRange` | Range指定で部分取得できること |
| `TestHeadObject` | オブジェクトのメタデータが取得できること |
| `TestDeleteObject` | オブジェクトの削除が成功すること |
| `TestListObjectsV2` | オブジェクト一覧が正しく返ること |
| `TestListObjectsV2Prefix` | Prefix指定でフィルタできること |
| `TestListObjectsV2Pagination` | ページネーションが正しく動作すること |

**認証**
| テストケース | 検証内容 |
|-------------|---------|
| `TestValidSignatureV4` | 正しい署名でアクセスできること |
| `TestInvalidSignatureV4` | 不正な署名で403が返ること |
| `TestExpiredSignature` | 期限切れ署名で403が返ること |

**エラーレスポンス**
| テストケース | 検証内容 |
|-------------|---------|
| `TestErrorResponseFormat` | エラーがS3形式のXMLで返ること |
| `TestErrorCodes` | エラーコードがS3と一致すること |

#### Phase 2 テスト

**マルチパートアップロード**
| テストケース | 検証内容 |
|-------------|---------|
| `TestCreateMultipartUpload` | マルチパートアップロード開始 |
| `TestUploadPart` | パートアップロード |
| `TestCompleteMultipartUpload` | マルチパートアップロード完了 |
| `TestAbortMultipartUpload` | マルチパートアップロード中止 |
| `TestListParts` | パート一覧取得 |

### テスト実行方法

```bash
# S3互換性テストの実行
make test-s3compat

# 特定のテストのみ実行
go test -v ./test/s3compat/... -run TestCreateBucket

# カバレッジ付きで実行
make test-s3compat-coverage
```

### テストヘルパー

```go
// test/testutil/server.go
package testutil

import (
    "context"
    "testing"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/credentials"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestServer はテスト用のJOGサーバーを起動・管理する
type TestServer struct {
    Endpoint  string
    AccessKey string
    SecretKey string
}

// NewTestServer は一時ポートでサーバーを起動する
func NewTestServer(t *testing.T) *TestServer

// S3Client はテスト用のS3クライアントを返す
func (ts *TestServer) S3Client(t *testing.T) *s3.Client

// Cleanup はサーバーを停止しデータを削除する
func (ts *TestServer) Cleanup()
```

### CI統合

```yaml
# .github/workflows/test.yml
name: Test
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@8e8c483db84b4bee98b60c0593521ed34d9990e8 # v6.0.1
      - uses: actions/setup-go@7a3fe6cf4cb3a834922a1244abfce67bcef6a0c5 # v6.2.0
        with:
          go-version: '1.25'
      - name: Run unit tests
        run: make test
      - name: Run S3 compatibility tests
        run: make test-s3compat
```

### 依存ライブラリ (テスト用追加)

| ライブラリ | 用途 |
|-----------|------|
| `github.com/aws/aws-sdk-go-v2` | S3互換性テスト用クライアント |
| `github.com/aws/aws-sdk-go-v2/service/s3` | S3 API操作 |
| `github.com/stretchr/testify` | テストアサーション |

---

## 実装優先順位

### MVP (最小実行可能製品) のスコープ

1. **CLIの基本構造** - cobra による CLI
2. **HTTPサーバー** - 基本的なルーティング
3. **CreateBucket / ListBuckets** - バケット操作の基本
4. **PutObject / GetObject** - オブジェクトの読み書き
5. **ListObjectsV2** - オブジェクト一覧
6. **DeleteBucket / DeleteObject** - 削除操作
7. **AWS Signature V4認証** - 認証基盤
8. **S3互換性テスト** - AWS SDK for Go v2によるテストスイート

MVPでAWS CLIから基本操作が可能な状態を目指す。
各機能実装時には対応するS3互換性テストを同時に作成し、互換性を担保する。
