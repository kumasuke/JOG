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
- [ ] `jog server` - サーバー起動コマンド
- [ ] `jog config` - 設定管理
- [ ] `jog version` - バージョン表示

#### 1.2 基本的なS3 API
**バケット操作**
- [ ] `PUT /{bucket}` - CreateBucket
- [ ] `DELETE /{bucket}` - DeleteBucket
- [ ] `GET /` - ListBuckets
- [ ] `HEAD /{bucket}` - HeadBucket

**オブジェクト操作**
- [ ] `PUT /{bucket}/{key}` - PutObject
- [ ] `GET /{bucket}/{key}` - GetObject
- [ ] `DELETE /{bucket}/{key}` - DeleteObject
- [ ] `GET /{bucket}?list-type=2` - ListObjectsV2
- [ ] `HEAD /{bucket}/{key}` - HeadObject

#### 1.3 ストレージバックエンド
- [ ] ローカルファイルシステムバックエンド
- [ ] メタデータ管理 (SQLite)

#### 1.4 認証
- [ ] Access Key / Secret Key認証 (AWS Signature V4)

#### 1.5 S3互換性テスト
- [ ] AWS SDK for Go v2 を使った統合テスト
- [ ] 全APIエンドポイントの互換性テスト
- [ ] エラーレスポンス形式の互換性テスト

### Phase 2: 機能拡充

#### 2.1 マルチパートアップロード
- [ ] CreateMultipartUpload
- [ ] UploadPart
- [ ] CompleteMultipartUpload
- [ ] AbortMultipartUpload
- [ ] ListParts
- [ ] ListMultipartUploads

#### 2.2 追加オブジェクト操作
- [ ] CopyObject
- [ ] DeleteObjects (一括削除)
- [ ] GetObjectAttributes

#### 2.3 バケットポリシー
- [ ] PutBucketPolicy
- [ ] GetBucketPolicy
- [ ] DeleteBucketPolicy

#### 2.4 バージョニング
- [ ] PutBucketVersioning
- [ ] GetBucketVersioning
- [ ] オブジェクトバージョン管理

### Phase 3: 運用機能

- [ ] アクセスログ
- [ ] メトリクス (Prometheus形式)
- [ ] ヘルスチェックエンドポイント
- [ ] TLS対応
- [ ] CORS設定

### Phase 4: WebUI (将来)

- [ ] ダッシュボード
- [ ] バケット/オブジェクトブラウザ
- [ ] ユーザー管理画面

### Phase 5: エッジ対応 (将来)

- [ ] 軽量化ビルド
- [ ] ARM対応
- [ ] レプリケーション機能

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
│   │   └── metadata.go
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
| `github.com/spf13/viper` | 設定管理 |
| `github.com/mattn/go-sqlite3` | メタデータDB |
| `github.com/google/uuid` | UUID生成 |
| `github.com/rs/zerolog` | ロギング |
| `github.com/aws/aws-sdk-go-v2` | S3互換性テスト |
| `github.com/aws/aws-sdk-go-v2/service/s3` | S3 API テスト |
| `github.com/stretchr/testify` | テストアサーション |

## 設定

### 環境変数

| 変数名 | 説明 | デフォルト |
|--------|------|-----------|
| `JOG_DATA_DIR` | データ保存ディレクトリ | `./data` |
| `JOG_PORT` | リッスンポート | `9000` |
| `JOG_ACCESS_KEY` | アクセスキー | `minioadmin` |
| `JOG_SECRET_KEY` | シークレットキー | `minioadmin` |
| `JOG_LOG_LEVEL` | ログレベル | `info` |

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
```

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

1. **CLIの基本構造** - cobra/viperによるCLI
2. **HTTPサーバー** - 基本的なルーティング
3. **CreateBucket / ListBuckets** - バケット操作の基本
4. **PutObject / GetObject** - オブジェクトの読み書き
5. **ListObjectsV2** - オブジェクト一覧
6. **DeleteBucket / DeleteObject** - 削除操作
7. **AWS Signature V4認証** - 認証基盤
8. **S3互換性テスト** - AWS SDK for Go v2によるテストスイート

MVPでAWS CLIから基本操作が可能な状態を目指す。
各機能実装時には対応するS3互換性テストを同時に作成し、互換性を担保する。
