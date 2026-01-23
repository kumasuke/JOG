# JOG vs MinIO ベンチマーク分析レポート

**調査日**: 2026年1月23日
**環境**: macOS Darwin 24.6.0, Apple M2, 8コア

## 概要

JOGとMinIOのパフォーマンスを比較するため、Goの標準ベンチマークツールを使用してS3互換API操作の性能を測定した。

## テスト環境

| 項目 | 値 |
|------|-----|
| OS | macOS (darwin/arm64) |
| CPU | Apple M2 |
| Go | 標準ベンチマーク (`go test -bench`) |
| JOG | Docker (localhost:9200) |
| MinIO | Docker (localhost:9300) |
| 認証情報 | benchadmin/benchadmin |

## 結果サマリー

### レイテンシ比較 (μs/op = マイクロ秒/操作)

| 操作 | JOG | MinIO | 比率 | 優位 |
|------|-----|-------|------|------|
| PutObject (1KB) | 431 | 2,748 | 6.4x | **JOG** |
| PutObject (1MB) | 4,810 | 16,637 | 3.5x | **JOG** |
| GetObject (1KB) | 1,611 | 1,002 | 0.6x | MinIO |
| GetObject (1MB) | 1,231 | 1,560 | 1.3x | **JOG** |
| ListObjectsV2 | 71,906 | 2,128 | 0.03x | MinIO |
| MultipartUpload | 77,318 | 120,625 | 1.6x | **JOG** |

### スループット比較 (ops/sec)

| 操作 | JOG | MinIO | 優位 |
|------|-----|-------|------|
| PutObject (1KB) | 2,322 | 364 | **JOG** |
| PutObject (1MB) | 208 | 60 | **JOG** |
| GetObject (1KB) | 621 | 998 | MinIO |
| GetObject (1MB) | 812 | 641 | **JOG** |
| ListObjectsV2 | 14 | 470 | MinIO |
| MultipartUpload | 13 | 8 | **JOG** |

## 詳細分析

### JOGが優れている点

#### 1. 書き込み性能 (PutObject)

JOGは書き込み操作で圧倒的な優位性を示した。

- **1KBファイル**: JOGが**6.4倍高速**
- **1MBファイル**: JOGが**3.5倍高速**

この優位性は、JOGのシンプルなストレージ実装によるオーバーヘッドの少なさに起因すると考えられる。MinIOは分散ストレージ機能やエラスティックコーディングなどのエンタープライズ機能を持つため、単一ノードでの書き込み時にもオーバーヘッドが発生する。

#### 2. 大規模ファイルの読み取り (GetObject 1MB)

1MBファイルの読み取りではJOGが**1.3倍高速**。ファイルサイズが大きくなるほどJOGの効率的なファイルI/Oが活きる傾向がある。

#### 3. マルチパートアップロード

JOGが**1.6倍高速**。パーツの管理とマージ処理がシンプルな実装であるため。

### MinIOが優れている点

#### 1. ListObjectsV2

MinIOが**33.8倍高速**。これはJOGの最大の改善ポイント。

**原因分析**:
- JOGはSQLiteをメタデータストアとして使用
- 現在のクエリやインデックスが最適化されていない可能性
- メモリ割り当て数がJOGは55,654回/opに対し、MinIOは6,161回/op

**改善案**:
- SQLiteクエリの最適化
- インデックスの追加
- 結果のキャッシング

#### 2. 小規模ファイルの読み取り (GetObject 1KB)

MinIOが**1.6倍高速**。小さいファイルではファイルシステムのオーバーヘッドよりもMinIOの最適化されたバッファリングが効いていると考えられる。

## メモリ効率

| 操作 | JOG (B/op) | MinIO (B/op) | JOG (allocs/op) | MinIO (allocs/op) |
|------|------------|--------------|-----------------|-------------------|
| PutObject (1KB) | 43,459 | 44,561 | 619 | 646 |
| PutObject (1MB) | 76,492 | 77,706 | 619 | 646 |
| GetObject (1KB) | 53,126 | 54,683 | 697 | 721 |
| GetObject (1MB) | 52,937 | 54,680 | 693 | 721 |
| ListObjectsV2 | 2,018,168 | 252,951 | 55,654 | 6,161 |
| MultipartUpload | 443,427 | 455,228 | 3,873 | 4,050 |

JOGは基本操作（Put/Get/Multipart）でMinIOよりメモリ効率が良いが、ListObjectsV2では大幅に劣る。

## 総評

### JOGの強み

1. **書き込み性能**: 小〜中規模ファイルのアップロードで3〜6倍高速
2. **シンプルな実装**: オーバーヘッドが少なく、単一ノード環境で効率的
3. **メモリ効率**: 基本操作でのメモリ使用量が少ない

### 改善が必要な領域

1. **ListObjectsV2の最適化**: 現在の33倍の差を縮める必要がある
2. **小規模ファイルの読み取り**: キャッシング戦略の検討

### ユースケース適合性

| ユースケース | 推奨 | 理由 |
|-------------|------|------|
| 大量の小ファイル書き込み | JOG | 6.4倍高速な書き込み |
| バックアップストレージ | JOG | 書き込み重視、リスト操作は稀 |
| ファイル一覧を頻繁に取得 | MinIO | ListObjectsV2が33倍高速 |
| 分散環境 | MinIO | JOGは単一ノード向け |

## 付録: 生データ

### JOG

```
BenchmarkPutObject_1KB-8       28090      430580 ns/op    43459 B/op     619 allocs/op
BenchmarkPutObject_1MB-8        3550     4810054 ns/op    76492 B/op     619 allocs/op
BenchmarkGetObject_1KB-8       18303     1611303 ns/op    53126 B/op     697 allocs/op
BenchmarkGetObject_1MB-8        9895     1231065 ns/op    52937 B/op     693 allocs/op
BenchmarkListObjectsV2-8         162    71905724 ns/op  2018168 B/op   55654 allocs/op
BenchmarkMultipartUpload-8       192    77318401 ns/op   443427 B/op    3873 allocs/op
```

### MinIO

```
BenchmarkPutObject_1KB-8        7044     2747682 ns/op    44561 B/op     646 allocs/op
BenchmarkPutObject_1MB-8        1094    16637484 ns/op    77706 B/op     646 allocs/op
BenchmarkGetObject_1KB-8       10000     1001883 ns/op    54683 B/op     721 allocs/op
BenchmarkGetObject_1MB-8        8374     1560048 ns/op    54680 B/op     721 allocs/op
BenchmarkListObjectsV2-8        5778     2128457 ns/op   252951 B/op    6161 allocs/op
BenchmarkMultipartUpload-8        93   120624528 ns/op   455228 B/op    4050 allocs/op
```

## 関連Issue

- [#13](https://github.com/kumasuke/JOG/issues/13): warpベンチマーク実行時にPUTリクエストでAccess Deniedエラーが発生する
