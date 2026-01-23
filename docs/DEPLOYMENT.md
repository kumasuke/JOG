# JOG デプロイメントガイド

本ドキュメントでは、JOGの本番環境へのデプロイ方法と、高可用性構成について説明します。

## 目次

- [データ構造](#データ構造)
- [基本デプロイ](#基本デプロイ)
- [Litestream連携（メタデータレプリケーション）](#litestream連携メタデータレプリケーション)
- [Docker Compose構成例](#docker-compose構成例)
- [バックアップ戦略](#バックアップ戦略)

---

## データ構造

JOGは以下の2種類のデータを管理します：

```
$JOG_DATA_DIR/
├── metadata.db          # SQLite: メタデータ（バケット情報、オブジェクト属性など）
└── buckets/             # オブジェクト実データ
    ├── bucket-a/
    │   └── objects/
    └── bucket-b/
        └── objects/
```

| データ種別 | 保存場所 | 説明 |
|-----------|---------|------|
| メタデータ | `metadata.db` | SQLiteデータベース。バケット設定、オブジェクト属性、ACL、タグ等 |
| オブジェクト実データ | `buckets/*/objects/` | 実際のファイルコンテンツ |

---

## 基本デプロイ

### バイナリ直接実行

```bash
# ビルド
make build

# 環境変数設定
export JOG_PORT=9000
export JOG_DATA_DIR=/var/lib/jog
export JOG_ACCESS_KEY=your-access-key
export JOG_SECRET_KEY=your-secret-key
export JOG_LOG_LEVEL=info

# 起動
./bin/jog server
```

### systemd サービス

```ini
# /etc/systemd/system/jog.service
[Unit]
Description=JOG S3-compatible Object Storage
After=network.target

[Service]
Type=simple
User=jog
Group=jog
ExecStart=/usr/local/bin/jog server
Environment=JOG_PORT=9000
Environment=JOG_DATA_DIR=/var/lib/jog
Environment=JOG_ACCESS_KEY=your-access-key
Environment=JOG_SECRET_KEY=your-secret-key
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable jog
sudo systemctl start jog
```

---

## Litestream連携（メタデータレプリケーション）

[Litestream](https://litestream.io/)は、SQLiteデータベースをS3互換ストレージにストリーミングレプリケーションするツールです。JOGのメタデータDBをリアルタイムでバックアップできます。

### なぜLitestreamか

- **リアルタイムレプリケーション**: WAL（Write-Ahead Log）を継続的に同期
- **低オーバーヘッド**: JOGの性能への影響が最小限
- **障害復旧**: メタデータDBの迅速な復元が可能
- **分離されたツール**: JOG本体に依存を追加しない

### インストール

```bash
# macOS
brew install litestream

# Linux (Debian/Ubuntu)
wget https://github.com/benbjohnson/litestream/releases/download/v0.3.13/litestream-v0.3.13-linux-amd64.deb
sudo dpkg -i litestream-v0.3.13-linux-amd64.deb
```

### 設定ファイル

```yaml
# /etc/litestream.yml
dbs:
  - path: /var/lib/jog/metadata.db
    replicas:
      # S3へのレプリケーション
      - type: s3
        bucket: jog-backup
        path: metadata
        endpoint: https://s3.amazonaws.com  # または他のS3互換エンドポイント
        region: ap-northeast-1
        access-key-id: ${AWS_ACCESS_KEY_ID}
        secret-access-key: ${AWS_SECRET_ACCESS_KEY}
        sync-interval: 1s

      # ローカルディレクトリへのバックアップ（オプション）
      - type: file
        path: /backup/jog/metadata
        retention: 72h
```

### 起動方法

#### 方法1: Litestreamでラップして起動

```bash
litestream replicate -config /etc/litestream.yml -exec '/usr/local/bin/jog server'
```

この方法では：
- Litestreamが先にメタデータDBを復元（存在する場合）
- JOGサーバーを子プロセスとして起動
- バックグラウンドでレプリケーションを継続

#### 方法2: 別プロセスとして起動

```bash
# ターミナル1: Litestream
litestream replicate -config /etc/litestream.yml

# ターミナル2: JOG
./bin/jog server
```

### systemd 統合

```ini
# /etc/systemd/system/jog.service
[Unit]
Description=JOG S3-compatible Object Storage with Litestream
After=network.target

[Service]
Type=simple
User=jog
Group=jog
ExecStart=/usr/bin/litestream replicate -config /etc/litestream.yml -exec '/usr/local/bin/jog server'
Environment=JOG_PORT=9000
Environment=JOG_DATA_DIR=/var/lib/jog
Environment=JOG_ACCESS_KEY=your-access-key
Environment=JOG_SECRET_KEY=your-secret-key
Environment=AWS_ACCESS_KEY_ID=your-aws-key
Environment=AWS_SECRET_ACCESS_KEY=your-aws-secret
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### 復元

新しいサーバーでメタデータDBを復元する場合：

```bash
# S3からの復元
litestream restore -config /etc/litestream.yml /var/lib/jog/metadata.db

# その後JOGを起動
./bin/jog server
```

---

## Docker Compose構成例

### Dockerfile

```dockerfile
# Dockerfile
FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /jog ./cmd/jog

# ---
FROM alpine:3.21

RUN apk add --no-cache sqlite-libs ca-certificates

# Litestreamのインストール
ADD https://github.com/benbjohnson/litestream/releases/download/v0.3.13/litestream-v0.3.13-linux-amd64-static.tar.gz /tmp/litestream.tar.gz
RUN tar -xzf /tmp/litestream.tar.gz -C /usr/local/bin && rm /tmp/litestream.tar.gz

COPY --from=builder /jog /usr/local/bin/jog
COPY docker/litestream.yml /etc/litestream.yml
COPY docker/entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh

VOLUME /data
EXPOSE 9000

ENTRYPOINT ["/entrypoint.sh"]
```

### エントリポイントスクリプト

```bash
#!/bin/sh
# docker/entrypoint.sh

set -e

# Litestreamが設定されている場合はレプリケーション付きで起動
if [ -n "$LITESTREAM_REPLICA_URL" ]; then
    # 既存のバックアップから復元を試みる
    litestream restore -if-replica-exists -config /etc/litestream.yml /data/metadata.db

    # Litestreamでラップして起動
    exec litestream replicate -config /etc/litestream.yml -exec "jog server"
else
    # Litestreamなしで直接起動
    exec jog server
fi
```

### Litestream設定（Docker用）

```yaml
# docker/litestream.yml
dbs:
  - path: /data/metadata.db
    replicas:
      - url: ${LITESTREAM_REPLICA_URL}
        sync-interval: 1s
```

### Docker Compose

```yaml
# docker-compose.yml
services:
  jog:
    build: .
    ports:
      - "9000:9000"
    volumes:
      - jog-data:/data
    environment:
      JOG_PORT: 9000
      JOG_DATA_DIR: /data
      JOG_ACCESS_KEY: ${JOG_ACCESS_KEY:-minioadmin}
      JOG_SECRET_KEY: ${JOG_SECRET_KEY:-minioadmin}
      JOG_LOG_LEVEL: info
      # Litestreamレプリケーション先（オプション）
      # LITESTREAM_REPLICA_URL: s3://backup-bucket/jog/metadata
      # AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}
      # AWS_SECRET_ACCESS_KEY: ${AWS_SECRET_ACCESS_KEY}
    restart: unless-stopped

volumes:
  jog-data:
```

### 起動

```bash
# Litestreamなし
docker compose up -d

# Litestreamあり
JOG_ACCESS_KEY=mykey JOG_SECRET_KEY=mysecret \
LITESTREAM_REPLICA_URL=s3://backup-bucket/jog/metadata \
AWS_ACCESS_KEY_ID=xxx AWS_SECRET_ACCESS_KEY=yyy \
docker compose up -d
```

---

## バックアップ戦略

### 推奨構成

| データ種別 | バックアップ方法 | 頻度 |
|-----------|----------------|------|
| メタデータDB | Litestream（リアルタイム） | 継続的 |
| オブジェクト実データ | rsync / rclone | 定期（日次など） |

### オブジェクト実データのバックアップ

```bash
# rsyncでのバックアップ
rsync -av --delete /var/lib/jog/buckets/ /backup/jog/buckets/

# rcloneでS3へバックアップ
rclone sync /var/lib/jog/buckets/ s3:backup-bucket/jog/buckets/
```

### 復元手順

1. **メタデータDBの復元**（Litestream）
   ```bash
   litestream restore -config /etc/litestream.yml /var/lib/jog/metadata.db
   ```

2. **オブジェクト実データの復元**
   ```bash
   rsync -av /backup/jog/buckets/ /var/lib/jog/buckets/
   ```

3. **JOGサーバーの起動**
   ```bash
   systemctl start jog
   ```

---

## 注意事項

- **メタデータDBとオブジェクトデータの整合性**: 両方のバックアップタイミングが大きくずれると、メタデータには存在するがファイルがない状態が発生する可能性があります。定期バックアップの際は可能な限り同時に取得してください。
- **Litestreamの制限**: 複数のJOGインスタンスから同じSQLiteファイルに書き込むことはできません。単一サーバー構成を前提としています。
- **本番環境では認証情報を安全に管理**: 環境変数やシークレット管理ツール（Vault、AWS Secrets Manager等）を使用してください。
