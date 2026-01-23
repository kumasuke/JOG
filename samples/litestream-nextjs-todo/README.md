# Litestream + Next.js Todo App

JOG（S3互換オブジェクトストレージ）とLitestream（SQLiteレプリケーション）を組み合わせたデモアプリケーションです。

## アーキテクチャ

```
┌─────────────────────────────────────────────────────────────────┐
│                    Docker Compose Network                        │
│                                                                  │
│  ┌──────────────┐     ┌──────────────────────────────────────┐  │
│  │     JOG      │◀────│      Next.js + Litestream            │  │
│  │   Port:9000  │     │                                      │  │
│  │              │     │  ┌────────────┐  ┌───────────────┐   │  │
│  │  ┌────────┐  │     │  │ Litestream │──│   Next.js     │   │  │
│  │  │Bucket: │  │     │  │  replicate │  │  (Todo App)   │   │  │
│  │  │backups │  │     │  │   to JOG   │  │  SQLite DB    │   │  │
│  │  └────────┘  │     │  └────────────┘  └───────────────┘   │  │
│  └──────────────┘     │                       Port:3000      │  │
│                       └──────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

## 技術スタック

| カテゴリ | 技術 |
|---------|------|
| フロントエンド | Next.js 15 (App Router) |
| スタイリング | Tailwind CSS 4 |
| DB | SQLite + better-sqlite3 |
| ORM | Drizzle ORM |
| レプリケーション | Litestream |
| ストレージ | JOG (S3互換) |

## クイックスタート

### 1. 起動

```bash
cd samples/litestream-nextjs-todo
docker compose up --build
```

### 2. アクセス

- Todo App: http://localhost:3000
- JOG (S3): http://localhost:9000

## 機能テスト

### Todoアプリの操作

1. http://localhost:3000 にアクセス
2. 新しいTodoを追加
3. Todoの完了/未完了を切り替え
4. Todoを削除

### レプリケーションの確認

JOGのbackupsバケットにデータがレプリケートされていることを確認：

```bash
# AWS CLIを使用
aws --endpoint-url http://localhost:9000 s3 ls s3://backups/todos/ \
  --no-sign-request

# または aws configure で認証情報を設定済みの場合
aws --endpoint-url http://localhost:9000 s3 ls s3://backups/todos/
```

### 復元テスト

データが正しく復元されることを確認：

```bash
# アプリコンテナを停止・削除
docker compose stop app
docker compose rm -f app

# ボリュームを削除（データを失う）
docker compose down -v --remove-orphans
docker compose up jog -d  # JOGを先に起動

# 再起動（JOGからデータが復元される）
docker compose up app
```

ブラウザで http://localhost:3000 を開くと、以前作成したTodoが復元されています。

## 環境変数

| 変数名 | デフォルト値 | 説明 |
|--------|-------------|------|
| `JOG_ACCESS_KEY` | minioadmin | JOGのアクセスキー |
| `JOG_SECRET_KEY` | minioadmin | JOGのシークレットキー |

## ファイル構成

```
samples/litestream-nextjs-todo/
├── README.md
├── docker-compose.yml
├── jog.Dockerfile
├── .env.example
└── app/
    ├── Dockerfile
    ├── package.json
    ├── tsconfig.json
    ├── next.config.ts
    ├── drizzle.config.ts
    ├── litestream.yml
    ├── entrypoint.sh
    └── src/
        ├── app/
        │   ├── layout.tsx
        │   ├── page.tsx
        │   └── globals.css
        ├── components/
        │   ├── TodoList.tsx
        │   ├── TodoItem.tsx
        │   └── AddTodo.tsx
        ├── db/
        │   ├── index.ts
        │   └── schema.ts
        └── lib/
            └── actions.ts
```

## 開発

### ローカル開発（Docker外）

```bash
cd app
npm install
npm run dev
```

注意: ローカル開発時はLitestreamは動作しません。

### マイグレーションの追加

スキーマを変更した場合：

```bash
cd app
npm run db:generate
```

## トラブルシューティング

### JOGに接続できない

- JOGコンテナが起動しているか確認: `docker compose ps`
- ヘルスチェック: `curl http://localhost:9000/health`

### データが復元されない

- backupsバケットにデータがあるか確認
- Litestreamのログを確認: `docker compose logs app`

## 参考資料

- [JOG - S3互換オブジェクトストレージ](../../README.md)
- [Litestream](https://litestream.io/)
- [Next.js](https://nextjs.org/)
- [Drizzle ORM](https://orm.drizzle.team/)
