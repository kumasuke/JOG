# ライフサイクルエンジン 性能計測ガイド

ライフサイクル実行エンジン（`internal/lifecycle`）の性能を計測・再現するための手順をまとめる。

> **重要な前提**: Warp（MinIO 公式の S3 ベンチ）による JOG vs MinIO 比較は **エンジンの性能を測れない**。エンジンは既定 1h 間隔・起動 1 分後に初回実行のため、数分の Warp 走行中は発火しないからである。エンジン固有の性能は以下の **in-process Go ベンチ** が一次情報になる。Warp 比較は JOG の一般的な S3 スループットを見るための補助に留める。

---

## 1. エンジン固有の in-process ベンチ（一次情報）

ガード付き削除・トランザクション primitive・書き込みパス競合を分離計測する。

```bash
# 単発削除のスループットと tx オーバーヘッド、競合下の書き込み
go test ./internal/storage/ -run '^$' \
  -bench 'ExpireObjectVersionGuarded|DeleteObjectVersioned|WithImmediateTxNoop|PutObjectVersioned' \
  -benchmem -benchtime=2s

# 1 サイクルのスキャンコスト（2000 キー・該当なし）
go test ./internal/lifecycle/ -run '^$' -bench RunOnce_ScanNoEligible -benchmem -benchtime=3s
```

代表値（Apple M2、参考）:

| ベンチ | 結果 | 意味 |
|---|---|---|
| `ExpireObjectVersionGuarded` | ~455µs/op | ガード付き削除（既存の `DeleteObjectVersioned` 505µs より速い） |
| `withImmediateTx`（空） | ~8µs/op | ガード primitive の固定オーバーヘッド |
| `RunOnce` スキャン（2000キー） | ~86ms/cycle | 該当なし時の定常コスト（≒43µs/キー） |
| Put 無競合 / エンジン競合下 | 3.8ms / 4.3ms（BUSYリトライ 0） | WAL+busy_timeout 修正後。競合影響は軽微 |

---

## 2. 大規模スキャンのスケーリング検証

`object_versions` に行を一括 INSERT（2 バージョン/キー・データファイル無し）して、サイクルのスキャン時間がキー数に対して線形にスケールするかを実測する。100 万キーでも数十秒で計測できる。

```bash
# 10万・100万キーで各 1 サイクル
go test ./internal/lifecycle/ -run '^$' -bench RunOnce_ScanScale -benchtime=1x -timeout 600s
```

代表値（Apple M2、2 バージョン/キー、該当なし・1 サイクル）:

| キー数 | スキャン/cycle | per-key |
|---|---|---|
| 100,000 | ~1.9s | ~19µs |
| 1,000,000 | ~21.6s | ~21.5µs |

per-key コストがほぼ一定で**線形スケール**する。

> **このベンチで見つけて直した O(n²) バグ**: 当初 `ListLifecycleObjectKeys` の `objects UNION object_versions` ページングは各 UNION ブランチに `LIMIT` を押し下げておらず、ページごとに「残り全キー」を走査していた（100 万キーで 384s）。各ブランチに `ORDER BY key LIMIT`（object_versions は `DISTINCT key`）を押し下げて線形化（100 万キーが 384s → 21.6s、約 18 倍）。

スキャンは keyset pagination ＋ キーごとの `GetObjectVersionsForKey` で構成され、メモリはキー単位で有界（全件を一度に載せない）。`max_actions_per_cycle` は削除数を上限するが**スキャン自体は上限しない**ため、巨大バケットの初回サイクルはキー数に比例した時間がかかる。日次〜時間次の周期に対しては十分高速だが、数千万キー級では `interval` を長めにする運用も検討する。

---

## 3. Warp で JOG vs MinIO 総合スループット比較（補助）

```bash
cd benchmark
./scripts/install-warp.sh              # bin/warp をDL
./scripts/run-all.sh both mixed        # JOG+MinIO, 混合70/30, 約3〜4分
# 詳細: ./scripts/run-all.sh both throughput   (約20分)
# フル: ./scripts/run-all.sh all all            (30分以上)
```

- 前提: Docker / Warp CLI / 各サーバーイメージ（初回は pull でネット必要）。
- 結果は `benchmark/results/` に出力される。
- **再掲の注意**: これは一般的な S3 スループット比較であり、ライフサイクルエンジンの性能・競合影響は測定できない。

<!-- RESULTS:WARP -->

---

## 4. 実オブジェクト大量 seed ＋ 稼働下負荷（重い・任意）

エンジン稼働下でリクエストパスのレイテンシ影響を実環境で測る、最も重い試験。数百万オブジェクトの作成にディスク・時間を大きく消費する。

手順の概要:

1. JOG をエンジン有効で起動（`docker compose up -d` または `jog server`）。
2. 大量オブジェクトを seed する。最速は **メタデータ DB への直接一括 INSERT**（§2 の seeder と同方式。`last_modified` を過去日付にすれば期限切れも作れる）。実 S3 経由で作るなら AWS SDK の並列 PUT（実測 ~4ms/個 ＝ 100 万で ~1 時間）。
3. ライフサイクル設定を投入（Expiration / NoncurrentVersionExpiration / AbortIncompleteMultipartUpload）。
4. Warp の `mixed` 等でリクエスト負荷をかけながら、`jog lifecycle run` を別途実行（または内蔵 ticker の発火を待つ）。
5. Warp のレイテンシ p50/p99 と JOG ログの `lifecycle bucket cycle complete`（actions / skipped_locked / errors）を突き合わせる。
6. **最上位制約の確認**: COMPLIANCE/GOVERNANCE 保持期限内・legal hold ON のバージョンに retention/legal hold を付け、サイクル後も `GetObject(versionId)` で取得できることを確認（`skipped_locked` がカウントされる）。

期限切れを即時に作るための backdate は、サーバーを正常停止（WAL チェックポイント）してから host の `sqlite3` で `object_versions.last_modified` の日付部分だけを過去に置換するのが簡単（書式は Go の `time.Time.String()` 形式を維持すること）。

---

## 参考

- 設計と実装後の所見: `docs/LIFECYCLE_ENGINE_DESIGN.md`（§8 codex レビュー、§9 ベンチ/WAL 所見）
- Warp の読み方: `benchmark/docs/WARP_ANALYSIS.md`
- 4 サーバー比較: `benchmark/docs/S3_ALTERNATIVES_COMPARISON.md`
