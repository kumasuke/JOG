# CLAUDE.md - Benchmark Guidelines

## 作業ディレクトリ

ベンチマーク関連の操作は、すべて `benchmark` ディレクトリ内で実行する。

```bash
cd benchmark
```

## クイックスタート（一括実行）

```bash
# 最短の動作確認（約1〜2分）
./scripts/run-all.sh jog mixed --skip-custom --skip-report

# 通常の開発時（約5分）
./scripts/run-all.sh jog mixed

# フルベンチマーク（20分以上）
./scripts/run-all.sh both all

# 全サーバーでベンチマーク（30分以上）
./scripts/run-all.sh all all
```

### シナリオと所要時間

| シナリオ | 所要時間（1ターゲット） | 用途 |
|---------|------------------------|------|
| `mixed` | **約1〜2分** | クイック動作確認、CI/CD |
| `concurrency` | 約8分 | スケーラビリティ評価 |
| `throughput` | 約10分以上 | 詳細な性能特性分析 |
| `all` | **20分以上** | フルベンチマーク |

### その他のオプション

```bash
# クリーンスタート（ボリューム削除してから実行）
./scripts/run-all.sh both all --clean

# コンテナを停止せずに終了
./scripts/run-all.sh both all -k

# Warpのみ実行（カスタムGoベンチマークをスキップ）
./scripts/run-all.sh both all --skip-custom
```

## Warp CLI

### インストール

インストールスクリプトを使用する（brew/go installは使わない）:

```bash
./scripts/install-warp.sh
```

### 実行

```bash
# JOGとMinIOの両方でスループットベンチマーク
./scripts/run-warp.sh both throughput

# JOGのみ
./scripts/run-warp.sh jog throughput

# MinIOのみ
./scripts/run-warp.sh minio throughput
```

### 手動実行

```bash
./bin/warp put \
  --host=localhost:9200 \
  --access-key=benchadmin \
  --secret-key=benchadmin \
  --tls=false \
  --bucket=benchmark \
  --obj.size=1MB \
  --concurrent=16 \
  --duration=60s
```

## カスタムGoベンチマーク

```bash
go test -bench=. -benchmem -benchtime=10s ./custom/...
```

## ベンチマーク環境

### 起動

```bash
docker compose -f docker-compose.benchmark.yml up -d
```

### 停止

```bash
docker compose -f docker-compose.benchmark.yml down
```

### エンドポイント

| サーバー | API | Console |
|---------|-----|---------|
| JOG | http://localhost:9200 | - |
| MinIO | http://localhost:9300 | http://localhost:9301 |
| rclone | http://localhost:9400 | - |
| versitygw | http://localhost:9500 | - |

### 認証情報

- Access Key: `benchadmin`
- Secret Key: `benchadmin`

## 結果ファイル

- Warp結果: `results/warp_*.json`
- Goベンチマーク結果: `results/*.txt`
- 分析レポート:
  - `docs/BENCHMARK_ANALYSIS.md` - JOG vs MinIO詳細分析
  - `docs/WARP_ANALYSIS.md` - Warp結果の読み方
  - `docs/S3_ALTERNATIVES_COMPARISON.md` - 4サーバー比較（JOG, MinIO, rclone, versitygw）

## 注意事項

- `bin/warp` と `*.json.zst` は `.gitignore` に含まれている
- 結果ファイルも `.gitignore` に含まれている（`results/*.json`, `results/*.txt`）
- 分析レポートはコミット対象
