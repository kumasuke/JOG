# Manual Testing Guide

AWS CLIを使用してJOGサーバーの機能を手動確認するためのガイドです。

## 前提条件

- AWS CLI (`aws` コマンド)
- JOGがビルド済み (`make build`)

## セットアップ

### 1. サーバー起動

```bash
# ビルド
make build

# サーバー起動（フォアグラウンド）
./bin/jog server

# または バックグラウンドで起動
./bin/jog server &
```

### 2. 環境変数設定

```bash
export AWS_ACCESS_KEY_ID=minioadmin
export AWS_SECRET_ACCESS_KEY=minioadmin
export AWS_DEFAULT_REGION=us-east-1
```

### 3. エイリアス設定（オプション）

```bash
alias jog-aws='aws --endpoint-url http://localhost:9000'
```

---

## 基本操作の確認

### バケット操作

```bash
# バケット作成
aws --endpoint-url http://localhost:9000 s3 mb s3://test-bucket

# バケット一覧
aws --endpoint-url http://localhost:9000 s3 ls

# バケット削除
aws --endpoint-url http://localhost:9000 s3 rb s3://test-bucket
```

### オブジェクト操作

```bash
# オブジェクトアップロード
echo "Hello, World!" | aws --endpoint-url http://localhost:9000 s3 cp - s3://test-bucket/hello.txt

# オブジェクト一覧
aws --endpoint-url http://localhost:9000 s3 ls s3://test-bucket/

# オブジェクトダウンロード
aws --endpoint-url http://localhost:9000 s3 cp s3://test-bucket/hello.txt -

# オブジェクト削除
aws --endpoint-url http://localhost:9000 s3 rm s3://test-bucket/hello.txt
```

---

## 高度な操作の確認

### DeleteObjects（一括削除）

```bash
# テストデータ作成
aws --endpoint-url http://localhost:9000 s3 mb s3://test-bucket
echo "content1" | aws --endpoint-url http://localhost:9000 s3 cp - s3://test-bucket/obj1.txt
echo "content2" | aws --endpoint-url http://localhost:9000 s3 cp - s3://test-bucket/obj2.txt
echo "content3" | aws --endpoint-url http://localhost:9000 s3 cp - s3://test-bucket/obj3.txt

# 一括削除
aws --endpoint-url http://localhost:9000 s3api delete-objects \
  --bucket test-bucket \
  --delete '{"Objects": [{"Key": "obj1.txt"}, {"Key": "obj2.txt"}]}'

# 結果確認
aws --endpoint-url http://localhost:9000 s3 ls s3://test-bucket/
```

期待される結果:
- `obj1.txt` と `obj2.txt` が削除される
- `obj3.txt` のみ残る

### CopyObject（サーバーサイドコピー）

```bash
# 同一バケット内コピー
aws --endpoint-url http://localhost:9000 s3api copy-object \
  --bucket test-bucket \
  --copy-source test-bucket/obj3.txt \
  --key obj3-copy.txt

# 別バケットへコピー
aws --endpoint-url http://localhost:9000 s3 mb s3://dest-bucket
aws --endpoint-url http://localhost:9000 s3api copy-object \
  --bucket dest-bucket \
  --copy-source test-bucket/obj3.txt \
  --key copied.txt

# 結果確認
aws --endpoint-url http://localhost:9000 s3 ls s3://test-bucket/
aws --endpoint-url http://localhost:9000 s3 ls s3://dest-bucket/
```

期待される結果:
- `obj3-copy.txt` が `test-bucket` に作成される
- `copied.txt` が `dest-bucket` に作成される
- 元の `obj3.txt` は残る

### ListMultipartUploads（進行中アップロード一覧）

```bash
# マルチパートアップロードを開始
UPLOAD_ID=$(aws --endpoint-url http://localhost:9000 s3api create-multipart-upload \
  --bucket test-bucket --key large-file.bin --query 'UploadId' --output text)
echo "Upload ID: $UPLOAD_ID"

# 進行中アップロード一覧
aws --endpoint-url http://localhost:9000 s3api list-multipart-uploads \
  --bucket test-bucket

# プレフィックスでフィルタリング
aws --endpoint-url http://localhost:9000 s3api list-multipart-uploads \
  --bucket test-bucket --prefix "docs/"

# クリーンアップ（アップロード中止）
aws --endpoint-url http://localhost:9000 s3api abort-multipart-upload \
  --bucket test-bucket --key large-file.bin --upload-id "$UPLOAD_ID"
```

期待される結果:
- `Uploads` 配列に進行中のアップロード情報が含まれる
- `Key`, `UploadId`, `Initiated` が返される

### UploadPartCopy（パートコピー）

```bash
# ソースオブジェクト作成（100KB）
dd if=/dev/urandom bs=1024 count=100 2>/dev/null | \
  aws --endpoint-url http://localhost:9000 s3 cp - s3://test-bucket/source.bin

# マルチパートアップロード開始
UPLOAD_ID=$(aws --endpoint-url http://localhost:9000 s3api create-multipart-upload \
  --bucket test-bucket --key dest.bin --query 'UploadId' --output text)

# パートコピー
PART_RESULT=$(aws --endpoint-url http://localhost:9000 s3api upload-part-copy \
  --bucket test-bucket \
  --key dest.bin \
  --upload-id "$UPLOAD_ID" \
  --part-number 1 \
  --copy-source test-bucket/source.bin)
echo "$PART_RESULT"

ETAG=$(echo "$PART_RESULT" | jq -r '.CopyPartResult.ETag')

# マルチパートアップロード完了
aws --endpoint-url http://localhost:9000 s3api complete-multipart-upload \
  --bucket test-bucket \
  --key dest.bin \
  --upload-id "$UPLOAD_ID" \
  --multipart-upload "{\"Parts\": [{\"PartNumber\": 1, \"ETag\": $ETAG}]}"

# 結果確認
aws --endpoint-url http://localhost:9000 s3 ls s3://test-bucket/dest.bin
```

期待される結果:
- `CopyPartResult` に `ETag` と `LastModified` が含まれる
- 完了後、`dest.bin` が作成される（ソースと同じサイズ）

---

## クリーンアップ

```bash
# サーバー停止
pkill -f "jog server"

# データディレクトリ削除
rm -rf ./data
```

---

## トラブルシューティング

### サーバーに接続できない

```bash
# サーバーが起動しているか確認
curl http://localhost:9000/

# ポートが使用されているか確認
lsof -i :9000
```

### 認証エラー

```bash
# 環境変数が正しく設定されているか確認
echo $AWS_ACCESS_KEY_ID
echo $AWS_SECRET_ACCESS_KEY

# デフォルト認証情報
# Access Key: minioadmin
# Secret Key: minioadmin
```

### XMLパースエラー

- リクエストボディのJSON形式を確認
- `--delete` や `--multipart-upload` の引用符エスケープを確認
