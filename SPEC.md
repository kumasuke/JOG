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
├── test/                     # E2Eテスト
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
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

## 実装優先順位

### MVP (最小実行可能製品) のスコープ

1. **CLIの基本構造** - cobra/viperによるCLI
2. **HTTPサーバー** - 基本的なルーティング
3. **CreateBucket / ListBuckets** - バケット操作の基本
4. **PutObject / GetObject** - オブジェクトの読み書き
5. **ListObjectsV2** - オブジェクト一覧
6. **DeleteBucket / DeleteObject** - 削除操作
7. **AWS Signature V4認証** - 認証基盤

MVPでAWS CLIから基本操作が可能な状態を目指す。
