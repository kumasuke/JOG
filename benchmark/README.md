# JOG ベンチマーク実行ガイド

## 概要

このディレクトリには、JOGのパフォーマンスを測定するためのベンチマーク環境が含まれています。Warp（MinIO公式のS3ベンチマークツール）とカスタムGoベンチマークを使用して、JOGとMinIOのパフォーマンスを比較できます。

**注意**: ベンチマーク関連の操作は、すべて `benchmark` ディレクトリ内で実行してください。

```bash
cd benchmark
```

## クイックスタート

```bash
# 1. Warpをインストール（bin/にダウンロード）
./scripts/install-warp.sh

# 2. ベンチマーク環境を起動
docker compose -f docker-compose.benchmark.yml up -d

# 3. Warpベンチマークを実行
./scripts/run-warp.sh both throughput

# 4. カスタムGoベンチマークを実行
go test -bench=. -benchmem -benchtime=10s ./custom/...
```

## ベンチマークツール

- **Warp**: MinIO公式のS3ベンチマークツール。実際のS3ワークロードをシミュレート
- **カスタムGoベンチマーク**: Go標準のベンチマーク機能を使用した詳細な性能測定

## 前提条件

以下のツールがインストールされている必要があります:

- Docker（バージョン20.10以上）
- Docker Compose（バージョン2.0以上）
- Go（バージョン1.23以上）
- Warp CLI

### Warp CLIのインストール

```bash
./scripts/install-warp.sh

# バージョン指定も可能
./scripts/install-warp.sh v1.4.0
```

スクリプトは以下を自動で行います:
- OSとアーキテクチャを検出（macOS/Linux、amd64/arm64）
- MinIO公式サイトからバイナリをダウンロード
- `bin/warp` に配置
- 実行権限を付与

**インストール確認**

```bash
./bin/warp --version
```

## 環境セットアップ

### 1. ベンチマーク環境の起動

```bash
# benchmark ディレクトリに移動
cd benchmark

# JOGとMinIOを起動
docker compose -f docker-compose.benchmark.yml up -d

# ログ確認（ヘルスチェック成功まで待機）
docker compose -f docker-compose.benchmark.yml logs -f
```

起動確認:
- JOG: http://localhost:9200
- MinIO API: http://localhost:9300
- MinIO Console: http://localhost:9301

認証情報（共通）:
- Access Key: `benchadmin`
- Secret Key: `benchadmin`

### 2. 環境のクリーンアップ

```bash
# コンテナ停止
docker compose -f docker-compose.benchmark.yml down

# データも削除する場合
docker compose -f docker-compose.benchmark.yml down -v
```

## Warpベンチマークの実行

### 基本的な使い方

```bash
# JOGに対するベンチマーク（PUTオペレーション）
warp put \
  --host=localhost:9200 \
  --access-key=benchadmin \
  --secret-key=benchadmin \
  --tls=false \
  --bucket=benchmark \
  --objects=1000 \
  --obj.size=1MB \
  --concurrent=32 \
  --duration=60s

# MinIOに対するベンチマーク（比較用）
warp put \
  --host=localhost:9300 \
  --access-key=benchadmin \
  --secret-key=benchadmin \
  --tls=false \
  --bucket=benchmark \
  --objects=1000 \
  --obj.size=1MB \
  --concurrent=32 \
  --duration=60s
```

### 各種ベンチマークシナリオ

#### 1. PUTベンチマーク（アップロード性能）

```bash
warp put \
  --host=localhost:9200 \
  --access-key=benchadmin \
  --secret-key=benchadmin \
  --tls=false \
  --bucket=benchmark \
  --obj.size=1MB \
  --concurrent=16 \
  --duration=60s
```

#### 2. GETベンチマーク（ダウンロード性能）

```bash
# まずデータを準備
warp put \
  --host=localhost:9200 \
  --access-key=benchadmin \
  --secret-key=benchadmin \
  --tls=false \
  --bucket=benchmark \
  --objects=1000 \
  --obj.size=1MB

# GETベンチマーク実行
warp get \
  --host=localhost:9200 \
  --access-key=benchadmin \
  --secret-key=benchadmin \
  --tls=false \
  --bucket=benchmark \
  --concurrent=16 \
  --duration=60s
```

#### 3. 混合ワークロード（GET 70% + PUT 30%）

```bash
warp mixed \
  --host=localhost:9200 \
  --access-key=benchadmin \
  --secret-key=benchadmin \
  --tls=false \
  --bucket=benchmark \
  --obj.size=1MB \
  --get-distrib=70 \
  --put-distrib=30 \
  --concurrent=32 \
  --duration=60s
```

#### 4. 複数オブジェクトサイズでのテスト

```bash
# 1KB
warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=benchmark --obj.size=1KB --concurrent=16 --duration=60s

# 64KB
warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=benchmark --obj.size=64KB --concurrent=16 --duration=60s

# 1MB
warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=benchmark --obj.size=1MB --concurrent=16 --duration=60s

# 16MB
warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=benchmark --obj.size=16MB --concurrent=16 --duration=60s

# 64MB
warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=benchmark --obj.size=64MB --concurrent=8 --duration=60s
```

#### 5. 並行度を変えたテスト

```bash
# 並行度 1（シングルスレッド）
warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=benchmark --obj.size=1MB --concurrent=1 --duration=60s

# 並行度 4
warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=benchmark --obj.size=1MB --concurrent=4 --duration=60s

# 並行度 16
warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=benchmark --obj.size=1MB --concurrent=16 --duration=60s

# 並行度 64
warp put --host=localhost:9200 --access-key=benchadmin --secret-key=benchadmin --tls=false --bucket=benchmark --obj.size=1MB --concurrent=64 --duration=60s
```

### Warpの主要オプション

- `--host`: S3エンドポイント
- `--access-key`: アクセスキー
- `--secret-key`: シークレットキー
- `--tls`: TLS使用（true/false）
- `--bucket`: バケット名
- `--objects`: オブジェクト数
- `--obj.size`: オブジェクトサイズ（1KB, 64KB, 1MB, 16MBなど）
- `--concurrent`: 並行スレッド数
- `--duration`: テスト実行時間（例: 60s, 5m）
- `--get-distrib`: 混合ワークロードでのGET比率（%）
- `--put-distrib`: 混合ワークロードでのPUT比率（%）

## カスタムGoベンチマークの実行

### 1. ベンチマークの実行

```bash
# すべてのベンチマークを実行
go test -bench=. -benchmem -benchtime=10s ./...

# 特定のベンチマークのみ実行
go test -bench=BenchmarkPutObject -benchmem -benchtime=10s

# 結果をファイルに保存
go test -bench=. -benchmem -benchtime=10s ./... | tee results/benchmark-$(date +%Y%m%d-%H%M%S).txt
```

### 2. ベンチマーク結果の比較

```bash
# benchstatツールのインストール
go install golang.org/x/perf/cmd/benchstat@latest

# JOGのベンチマーク実行
go test -bench=. -benchmem -benchtime=10s ./... > results/jog-bench.txt

# MinIOのベンチマーク実行（エンドポイント変更）
# 環境変数でエンドポイント指定
BENCHMARK_ENDPOINT=http://localhost:9300 go test -bench=. -benchmem -benchtime=10s ./... > results/minio-bench.txt

# 結果比較
benchstat results/jog-bench.txt results/minio-bench.txt
```

## 結果の解釈

### Warpの出力例

```
Operation: PUT
* Average: 145.32 MiB/s, 145.32 obj/s
* Throughput: min: 120.45 MiB/s, median: 145.32 MiB/s, max: 165.78 MiB/s
* Latency: min: 8.2ms, median: 110.5ms, max: 245.3ms, p99: 198.7ms
* Errors: 0
```

#### 重要な指標

- **Average**: 平均スループット（MiB/s）とオペレーション/秒
- **Throughput**: 最小・中央値・最大スループット
- **Latency**: レイテンシ（応答時間）
  - `median`: 中央値（50パーセンタイル）
  - `p99`: 99パーセンタイル（最も遅い1%を除いた最大値）
- **Errors**: エラー数（0が理想）

### Goベンチマークの出力例

```
BenchmarkPutObject-8    1000    1234567 ns/op    5432 B/op    123 allocs/op
```

#### 読み方

- `BenchmarkPutObject-8`: ベンチマーク名-並行度
- `1000`: 実行回数
- `1234567 ns/op`: 1オペレーションあたりのナノ秒
- `5432 B/op`: 1オペレーションあたりのメモリ割り当て（バイト）
- `123 allocs/op`: 1オペレーションあたりのメモリ割り当て回数

### パフォーマンス評価の基準

#### 小さなファイル（1KB - 64KB）
- レイテンシが重要（<10ms が理想的）
- オペレーション/秒が高いほど良い

#### 中程度のファイル（1MB - 16MB）
- スループットとレイテンシのバランス
- ネットワーク帯域幅の影響を考慮

#### 大きなファイル（64MB以上）
- スループット（MiB/s）が重要
- ディスクI/Oがボトルネックになる可能性

#### 並行度の影響
- 並行度が高いほどスループットは向上（一定レベルまで）
- 並行度が高すぎるとレイテンシが悪化
- 最適な並行度を見つけることが重要

## レポート生成

### 1. 結果の保存先

すべてのベンチマーク結果は `results/` ディレクトリに保存してください:

```bash
# Warp結果
warp put ... | tee results/warp-put-jog-$(date +%Y%m%d).txt

# Goベンチマーク結果
go test -bench=. ... > results/go-bench-$(date +%Y%m%d).txt
```

### 2. サマリーレポートの作成

主要な結果を `results/SUMMARY.md` にまとめることを推奨:

```markdown
# ベンチマーク結果サマリー

日付: 2026-01-23
環境: Docker on macOS (Apple M1)

## JOG vs MinIO

### PUTオペレーション（1MB, 並行度16）
- JOG: 145 MiB/s, レイテンシ中央値: 110ms
- MinIO: 320 MiB/s, レイテンシ中央値: 50ms

### GETオペレーション（1MB, 並行度16）
- JOG: 210 MiB/s, レイテンシ中央値: 76ms
- MinIO: 450 MiB/s, レイテンシ中央値: 35ms

## 考察
- JOGはMinIOと比較して約45%のスループット
- 小さなファイル（<64KB）ではレイテンシ差が顕著
- 今後の最適化ポイント: [具体的な改善案]
```

## ベンチマーク結果

### 2026-01-23: JOG vs MinIO (PR #15修正後)

**環境:**
- Docker on macOS (Apple Silicon)
- JOG: commit 9f46345
- MinIO: latest

**テスト条件:**
- Duration: 15秒
- Concurrency: 8
- Object Size: 1KiB

#### PUT Benchmark

| Server | Throughput | obj/s | Avg Latency | p99 Latency | Errors |
|--------|------------|-------|-------------|-------------|--------|
| **JOG** | 2.94 MiB/s | 3,014 | 2.6ms | 18.0ms | 0 |
| MinIO | 2.21 MiB/s | 2,259 | 3.6ms | 8.9ms | 0 |

**結果:**
- JOGはMinIOより約**33%高速**なスループットを達成
- JOGの平均レイテンシはMinIOより低い
- MinIOはp99レイテンシがより安定している

---

## トラブルシューティング

### コンテナが起動しない

```bash
# ログ確認
docker compose -f docker-compose.benchmark.yml logs

# ポート競合確認
lsof -i :9000
lsof -i :9100

# 完全クリーンアップ
docker compose -f docker-compose.benchmark.yml down -v
rm -rf data/
```

### Warpで接続エラー

```bash
# エンドポイント疎通確認
curl http://localhost:9200/
curl http://localhost:9300/

# バケット作成（手動）
aws s3 mb s3://benchmark \
  --endpoint-url http://localhost:9200 \
  --no-verify-ssl
```

### パフォーマンスが極端に悪い

- Dockerリソース制限を確認（Docker Desktop設定）
- ディスク空き容量を確認
- 他のプロセスによるCPU/メモリ使用を確認
- ログレベルを下げる（デバッグログは性能に影響）

## 参考資料

- [Warp公式ドキュメント](https://github.com/minio/warp)
- [Go Benchmarking](https://pkg.go.dev/testing#hdr-Benchmarks)
- [MinIO Performance Tuning](https://min.io/docs/minio/linux/operations/performance.html)
