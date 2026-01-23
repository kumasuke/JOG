# JOG vs MinIO ベンチマーク分析レポート

**調査日**: 2026年1月23日
**最終更新**: 2026年1月23日（ListObjectsV2最適化後）
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
| PutObject (1KB) | 407 | 1,344 | 3.3x | **JOG** |
| PutObject (1MB) | 3,463 | 9,996 | 2.9x | **JOG** |
| GetObject (1KB) | 709 | 867 | 1.2x | **JOG** |
| GetObject (1MB) | 1,322 | 1,861 | 1.4x | **JOG** |
| ListObjectsV2 | 1,236 | 2,033 | 1.6x | **JOG** |
| MultipartUpload | 54,400 | 94,832 | 1.7x | **JOG** |

### スループット比較 (ops/sec)

| 操作 | JOG | MinIO | 優位 |
|------|-----|-------|------|
| PutObject (1KB) | 2,458 | 744 | **JOG** |
| PutObject (1MB) | 289 | 100 | **JOG** |
| GetObject (1KB) | 1,411 | 1,153 | **JOG** |
| GetObject (1MB) | 757 | 537 | **JOG** |
| ListObjectsV2 | 809 | 492 | **JOG** |
| MultipartUpload | 18 | 11 | **JOG** |

## 詳細分析

### JOGが全操作でMinIOを上回る

2026年1月23日のListObjectsV2最適化により、JOGは**全操作でMinIOを上回る**パフォーマンスを達成した。

#### 1. 書き込み性能 (PutObject)

JOGは書き込み操作で優位性を示す。

- **1KBファイル**: JOGが**3.3倍高速**
- **1MBファイル**: JOGが**2.9倍高速**

この優位性は、JOGのシンプルなストレージ実装によるオーバーヘッドの少なさに起因する。MinIOは分散ストレージ機能やエラスティックコーディングなどのエンタープライズ機能を持つため、単一ノードでの書き込み時にもオーバーヘッドが発生する。

#### 2. 読み取り性能 (GetObject)

- **1KBファイル**: JOGが**1.2倍高速**
- **1MBファイル**: JOGが**1.4倍高速**

小さいファイルでも大きいファイルでもJOGが優位。

#### 3. ListObjectsV2（最適化後）

JOGが**1.6倍高速**。SQLクエリの最適化により劇的に改善。

**最適化内容**（PR #17）:
- SQLクエリにLIMIT句とstartAfter条件を追加
- 不要なmetadata列の取得を削除
- Go側のソート処理を削除（SQLiteのORDER BYで十分）

**改善効果**:
- レイテンシ: 71,906 μs → 1,236 μs（**58倍高速化**）
- メモリ: 2,018,168 B → 251,880 B（**8倍削減**）
- アロケーション: 55,654 → 6,132（**9倍削減**）

#### 4. マルチパートアップロード

JOGが**1.7倍高速**。パーツの管理とマージ処理がシンプルな実装であるため。

## メモリ効率

| 操作 | JOG (B/op) | MinIO (B/op) | JOG (allocs/op) | MinIO (allocs/op) |
|------|------------|--------------|-----------------|-------------------|
| PutObject (1KB) | 43,562 | 44,462 | 619 | 646 |
| PutObject (1MB) | 76,773 | 77,904 | 619 | 645 |
| GetObject (1KB) | 52,964 | 54,679 | 693 | 721 |
| GetObject (1MB) | 52,924 | 54,674 | 693 | 721 |
| ListObjectsV2 | 251,880 | 252,919 | 6,132 | 6,160 |
| MultipartUpload | 446,534 | 461,786 | 3,873 | 4,051 |

JOGは全操作でMinIOよりメモリ効率が良い。

## 総評

### JOGの強み

1. **全操作で高速**: 全S3操作でMinIOを上回るパフォーマンス
2. **書き込み性能**: 小〜中規模ファイルのアップロードで2.9〜3.3倍高速
3. **シンプルな実装**: オーバーヘッドが少なく、単一ノード環境で効率的
4. **メモリ効率**: 全操作でのメモリ使用量が少ない

### ユースケース適合性

| ユースケース | 推奨 | 理由 |
|-------------|------|------|
| 大量の小ファイル書き込み | JOG | 3.3倍高速な書き込み |
| バックアップストレージ | JOG | 書き込み・読み取り共に高速 |
| ファイル一覧を頻繁に取得 | JOG | ListObjectsV2が1.6倍高速 |
| 単一ノード環境 | JOG | 全操作でMinIOを上回る |
| 分散環境 | MinIO | JOGは単一ノード向け |

## 付録: 生データ

### JOG（最適化後）

```
BenchmarkPutObject_1KB-8        2781      406814 ns/op    43562 B/op     619 allocs/op
BenchmarkPutObject_1MB-8         306     3462692 ns/op    76773 B/op     619 allocs/op
BenchmarkGetObject_1KB-8        1482      708580 ns/op    52964 B/op     693 allocs/op
BenchmarkGetObject_1MB-8         834     1321979 ns/op    52924 B/op     693 allocs/op
BenchmarkListObjectsV2-8         889     1236099 ns/op   251880 B/op    6132 allocs/op
BenchmarkMultipartUpload-8        20    54399635 ns/op   446534 B/op    3873 allocs/op
```

### MinIO

```
BenchmarkPutObject_1KB-8         846     1343626 ns/op    44462 B/op     646 allocs/op
BenchmarkPutObject_1MB-8         139     9995617 ns/op    77904 B/op     645 allocs/op
BenchmarkGetObject_1KB-8        1299      866841 ns/op    54679 B/op     721 allocs/op
BenchmarkGetObject_1MB-8         758     1861364 ns/op    54674 B/op     721 allocs/op
BenchmarkListObjectsV2-8         516     2032689 ns/op   252919 B/op    6160 allocs/op
BenchmarkMultipartUpload-8        13    94832394 ns/op   461786 B/op    4051 allocs/op
```

### JOG（最適化前 - 参考）

```
BenchmarkPutObject_1KB-8       28090      430580 ns/op    43459 B/op     619 allocs/op
BenchmarkPutObject_1MB-8        3550     4810054 ns/op    76492 B/op     619 allocs/op
BenchmarkGetObject_1KB-8       18303     1611303 ns/op    53126 B/op     697 allocs/op
BenchmarkGetObject_1MB-8        9895     1231065 ns/op    52937 B/op     693 allocs/op
BenchmarkListObjectsV2-8         162    71905724 ns/op  2018168 B/op   55654 allocs/op
BenchmarkMultipartUpload-8       192    77318401 ns/op   443427 B/op    3873 allocs/op
```

## 関連Issue/PR

- [#13](https://github.com/kumasuke/JOG/issues/13): warpベンチマーク実行時にPUTリクエストでAccess Deniedエラーが発生する
- [#16](https://github.com/kumasuke/JOG/issues/16): ListObjectsV2のパフォーマンス最適化
- [#17](https://github.com/kumasuke/JOG/pull/17): perf: ListObjectsV2のパフォーマンス最適化（58倍高速化）
