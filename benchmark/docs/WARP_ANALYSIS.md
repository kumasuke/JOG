# Warp ベンチマーク分析ガイド

## 概要

Warpは MinIO公式のS3ベンチマークツールで、実際のS3ワークロードをシミュレートしてパフォーマンスを測定する。このドキュメントでは、Warpの結果の読み方と分析方法を説明する。

## Warpの特徴

- **リアルなワークロード**: 実際のS3クライアントと同様の動作をシミュレート
- **複数の操作タイプ**: PUT、GET、DELETE、LIST、混合ワークロードに対応
- **詳細な統計**: スループット、レイテンシ、エラー率を測定
- **比較機能**: 複数の結果を比較可能

## ベンチマークシナリオ

### 1. スループットテスト

異なるオブジェクトサイズでの性能を測定:

| サイズ | 用途 |
|--------|------|
| 1KB | メタデータ、小さな設定ファイル |
| 64KB | 小〜中規模のドキュメント |
| 1MB | 画像、一般的なファイル |
| 16MB | 動画、大きなドキュメント |
| 64MB | バックアップ、アーカイブ |

### 2. 並行度テスト

異なる並行接続数での性能を測定:

| 並行度 | 用途 |
|--------|------|
| 1 | シングルスレッドのベースライン |
| 4 | 軽量なバッチ処理 |
| 8 | 一般的なアプリケーション |
| 16 | 高負荷アプリケーション |
| 32 | サーバーサイド処理 |
| 64 | 最大負荷テスト |

### 3. 混合ワークロード

読み書きの比率を変えたテスト:

- **GET 70% / PUT 30%**: 読み取り中心（Webアプリケーション）
- **GET 50% / PUT 50%**: バランス型（ファイル同期）
- **GET 30% / PUT 70%**: 書き込み中心（ログ収集）

## 結果の読み方

### Warp出力例

```
Operation: PUT
* Average: 145.32 MiB/s, 145.32 obj/s
* Throughput: min: 120.45 MiB/s, median: 145.32 MiB/s, max: 165.78 MiB/s
* Latency: min: 8.2ms, median: 110.5ms, max: 245.3ms, p99: 198.7ms
* Errors: 0
```

### 指標の意味

| 指標 | 説明 | 重要度 |
|------|------|--------|
| **Average (MiB/s)** | 平均スループット（データ転送量） | 高 |
| **Average (obj/s)** | 平均オペレーション/秒 | 高 |
| **Throughput median** | スループットの中央値 | 高 |
| **Latency median** | レイテンシの中央値（50パーセンタイル） | 高 |
| **Latency p99** | 99パーセンタイルレイテンシ | 高 |
| **Errors** | エラー数（0が理想） | 高 |
| **Throughput min/max** | スループットの範囲 | 中 |
| **Latency min/max** | レイテンシの範囲 | 中 |

### 分析のポイント

#### スループット（MiB/s）
- **高いほど良い**
- 大きなファイルで重要
- ネットワーク帯域幅やディスクI/Oに依存

#### オペレーション/秒（obj/s）
- **高いほど良い**
- 小さなファイルで重要
- CPUやメモリに依存

#### レイテンシ（ms）
- **低いほど良い**
- `median`: 一般的なユーザー体験を反映
- `p99`: 最悪のケースを反映（SLA設計に重要）

## JOG vs MinIO 比較の視点

### JOGの特徴
- シンプルな実装でオーバーヘッドが少ない
- 単一ノード環境に最適化
- 小〜中規模のワークロードで高性能

### MinIOの特徴
- エンタープライズ機能（分散、冗長化）
- 大規模環境でスケール可能
- 単一ノードでもオーバーヘッドが発生

### 比較時の注意点

1. **公平な条件で比較**: 同じハードウェア、同じネットワーク条件
2. **複数回実行**: 結果のばらつきを考慮
3. **ウォームアップ**: 初回実行は除外
4. **リソース監視**: CPU、メモリ、ディスクI/Oを同時に監視

## ベンチマーク実行手順

### 1. 環境準備

```bash
cd benchmark
./scripts/install-warp.sh
docker compose -f docker-compose.benchmark.yml up -d
```

### 2. ベンチマーク実行

```bash
# スループットテスト（JOGとMinIO両方）
./scripts/run-warp.sh both throughput

# 並行度テスト
./scripts/run-warp.sh both concurrency

# 混合ワークロード
./scripts/run-warp.sh both mixed

# 全シナリオ
./scripts/run-warp.sh both all
```

### 3. 手動でのカスタム実行

```bash
# JOG: PUT 1MB, 並行度16, 60秒
./bin/warp put \
  --host=localhost:9200 \
  --access-key=benchadmin \
  --secret-key=benchadmin \
  --tls=false \
  --bucket=benchmark \
  --obj.size=1MB \
  --concurrent=16 \
  --duration=60s

# MinIO: 同じ条件
./bin/warp put \
  --host=localhost:9300 \
  --access-key=benchadmin \
  --secret-key=benchadmin \
  --tls=false \
  --bucket=benchmark \
  --obj.size=1MB \
  --concurrent=16 \
  --duration=60s
```

### 4. 結果の比較

Warpは `.json.zst` 形式で結果を保存する。比較には `warp cmp` コマンドを使用:

```bash
./bin/warp cmp warp-put-jog.json.zst warp-put-minio.json.zst
```

## 結果ファイル

### 保存場所

- 自動実行: `results/warp_*.json`
- 手動実行: カレントディレクトリに `.json.zst` ファイル

### ファイル命名規則

```
warp-{operation}-{date}[{time}]-{random}.json.zst
```

例: `warp-put-2026-01-23[144458]-CUZb.json.zst`

## トラブルシューティング

### エラー: "Access Denied"

認証情報が正しいか確認:
```bash
./bin/warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=test
```

### エラー: "Bucket does not exist"

バケットを作成:
```bash
aws s3 mb s3://benchmark --endpoint-url http://localhost:9200
```

### 結果が安定しない

- テスト時間を長くする（`--duration=120s`）
- `--autoterm` オプションで自動終了
- 複数回実行して平均を取る

## 参考リンク

- [Warp GitHub](https://github.com/minio/warp)
- [MinIO Performance Tuning](https://min.io/docs/minio/linux/operations/performance.html)
