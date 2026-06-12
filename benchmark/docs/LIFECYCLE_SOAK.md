# ライフサイクルエンジン ソークテスト手順書

長時間連続稼働での**負荷・安定性**を検証するための手順。(a) 数十分のミニソークと (b) 24h 本格ソークの 2 段階を記す。いつでも再実行できるよう、投入は再利用可能な Go ツール [`benchmark/soak/seed`](../soak/seed/README.md) を使う。

> in-process の性能ベンチ・スケール検証・Warp 比較は [`LIFECYCLE_PERF.md`](./LIFECYCLE_PERF.md) を参照。本書は「壊れずに回り続けるか」を見る soak に特化する。

---

## 0. 何を確認するか（pass/fail 基準）

| 観点 | 合格基準 |
|---|---|
| メモリリーク | コンテナ RSS が時間とともに**単調増加し続けない**（削除完了後は横ばい/微増） |
| WAL 肥大 | `metadata.db-wal` が**無制限に増えない**（チェックポイントで定期的に縮む） |
| クラッシュ/panic | プロセス再起動・panic ログが**ゼロ** |
| エラー率 | リクエストの 5xx・SQLITE_BUSY 由来エラーが**ゼロ近傍で一定**（経時悪化しない） |
| レイテンシ | Warp の p50/p99 がサイクル発火時に**スパイクし続けない**（一時的増は可） |
| 正当性 | 期待削除数だけ減り、`errors`=0、（ロック有り構成なら）`skipped_locked` がカウントされ保護対象が残る |

> 注: goroutine リーク検知には pprof が要る。JOG は現状 pprof エンドポイントを持たないため、本 soak は **RSS・WAL サイズ・エラー率・サイクルログ**で判定する。goroutine 単位で見たい場合は `cmd/jog` に `net/http/pprof` を一時的に組み込む（別タスク）。

---

## (a) ミニソーク（30〜60分・このマシンで完結）

粗いリーク・即死・明らかなドリフトを短時間で洗う。

### 手順

```bash
cd benchmark   # 以降パスは worktree ルート基準でも可

# 1. データ投入（20万キー × 2版 → 20万 noncurrent が削除対象）
HOST_DATA=/tmp/jog-soak
rm -rf "$HOST_DATA"; mkdir -p "$HOST_DATA"; chmod 777 "$HOST_DATA"
go run ./benchmark/soak/seed --data-dir "$HOST_DATA" --keys 200000 --versions 2 --age-days 10 --noncurrent-days 1

# 2. 短周期・有界アクションで JOG を起動（イメージは docker compose build 済みのものを使用）
docker run -d --name jog-soak -p 9000:9000 \
  -v "$HOST_DATA":/data \
  -e JOG_AUTH_ACCESS_KEY=minioadmin -e JOG_AUTH_SECRET_KEY=minioadmin \
  -e JOG_LIFECYCLE_INTERVAL=30s -e JOG_LIFECYCLE_MAX_ACTIONS=2000 \
  rustling-puzzling-naur-jog
# 初回サイクルは起動約1分後。30s 間隔・2000件/サイクル → 20万件を約100サイクル(≈50分)で処理

# 3. 並行リクエスト負荷（別ターミナル）。エンジンと書き込みの競合を作る
./benchmark/bin/warp mixed --host=localhost:9000 \
  --access-key=minioadmin --secret-key=minioadmin --tls=false \
  --obj.size=1KiB --concurrent=8 --duration=45m --no-color

# 4. 監視（別ターミナル、10秒間隔でサンプリング）
while true; do
  ts=$(date +%H:%M:%S)
  rss=$(docker stats --no-stream --format '{{.MemUsage}}' jog-soak)
  wal=$(docker exec jog-soak sh -c 'ls -l /data/metadata.db-wal 2>/dev/null | awk "{print \$5}"')
  errs=$(docker logs jog-soak 2>&1 | grep -c '"level":"error"')
  cyc=$(docker logs jog-soak 2>&1 | grep -c 'lifecycle bucket cycle complete')
  echo "$ts rss=$rss wal=${wal:-0} errors=$errs cycles=$cyc"
  sleep 10
done | tee "$HOST_DATA/soak-monitor.log"
```

### 判定

- 監視ログで `rss` が削除進行中に増えても、削除完了後に**頭打ち**になればOK（単調増加し続けるのはリーク疑い）。
- `wal` が定期的に小さくなる（WAL チェックポイント）こと。増え続けるなら checkpoint 不全。
- `errors`=0、Warp の Errors=0、`cycles` が増え続ける（停止していない）。
- 終了後 `docker logs jog-soak | grep panic` が空。

### 後片付け

```bash
docker rm -f jog-soak
# rm -rf "$HOST_DATA"  # 不要なら削除
```

---

## (b) 24h 本格ソーク（無人実行向け）

緩いリーク・WAL/Litestream の累積・日次パターンを捕まえる。ミニソークと同型で**規模と時間を拡大**する。

### 設計

- データ: 100万キー × 2版（100万 noncurrent）。`max_actions=1000`・`interval=60s` → 約1000サイクル≈17h で削除しきり、その後は「該当なしスキャンのみ」で残り時間を回す（削除あり／なし両局面の安定性を見る）。
- 負荷: Warp `mixed` を 24h 連続（または `--duration=24h`）。
- 監視: 上記サンプラを 60s 間隔で 24h、CSV/ログに残す。
- 任意: Litestream 連携を有効化（`LITESTREAM_REPLICA_URL` を設定）して、レプリカ側の WAL 累積・リストアを別途検証。

### 手順（差分のみ）

```bash
HOST_DATA=/var/tmp/jog-soak-24h
rm -rf "$HOST_DATA"; mkdir -p "$HOST_DATA"; chmod 777 "$HOST_DATA"
go run ./benchmark/soak/seed --data-dir "$HOST_DATA" --keys 1000000 --versions 2 --age-days 10 --noncurrent-days 1

docker run -d --name jog-soak24 -p 9000:9000 \
  -v "$HOST_DATA":/data \
  -e JOG_AUTH_ACCESS_KEY=minioadmin -e JOG_AUTH_SECRET_KEY=minioadmin \
  -e JOG_LIFECYCLE_INTERVAL=60s -e JOG_LIFECYCLE_MAX_ACTIONS=1000 \
  --restart=no \
  rustling-puzzling-naur-jog

# 負荷（バックグラウンド or 別セッション、24h）
nohup ./benchmark/bin/warp mixed --host=localhost:9000 \
  --access-key=minioadmin --secret-key=minioadmin --tls=false \
  --obj.size=1KiB --concurrent=8 --duration=24h --no-color \
  > "$HOST_DATA/warp.log" 2>&1 &

# 監視（60s 間隔、24h ぶん）
nohup sh -c 'while true; do
  echo "$(date +%FT%T) $(docker stats --no-stream --format "{{.MemUsage}};{{.CPUPerc}}" jog-soak24) wal=$(docker exec jog-soak24 sh -c "ls -l /data/metadata.db-wal 2>/dev/null | awk \"{print \\\$5}\"") err=$(docker logs jog-soak24 2>&1 | grep -c \"\\\"level\\\":\\\"error\\\"\")"
  sleep 60
done' > "$HOST_DATA/monitor.csv" 2>&1 &
```

### 判定（24h 後）

1. `docker logs jog-soak24 | grep -E 'panic|fatal'` が空、コンテナ再起動回数 0（`docker inspect -f '{{.RestartCount}}' jog-soak24`）。
2. `monitor.csv` の RSS が**長期トレンドで平坦**（削除局面の山の後、スキャンのみ局面で横ばい）。継続的右肩上がりはリーク。
3. WAL サイズが上限内で振動（checkpoint 効いている）。
4. `warp.log` の Errors=0、p99 レイテンシが終盤で初期と同水準。
5. `object_versions` の件数が期待値（=current のみ＝100万行）に収束。

### 後片付け

```bash
docker rm -f jog-soak24; pkill -f 'warp mixed'
```

---

## バリエーション

- **Object Lock 保護の持続検証**: seeder に代えて、ロック付きバージョンを AWS SDK で用意（[`LIFECYCLE_PERF.md`](./LIFECYCLE_PERF.md) §4 / Docker E2E の手順）し、soak 中ずっと `skipped_locked` がカウントされ保護対象が残り続けることを確認。
- **Expiration（current → delete marker）soak**: 現行版に対する Days ルール。seeder は NCVE 専用なので、current 版＋objects 行を持つデータが必要（AWS SDK 経由 seed か、seeder の拡張）。
- **AIMU soak**: `multipart_uploads.initiated` を backdate して未完了アップロードを大量に作る（seeder 拡張または直接 INSERT）。

## 関連

- 投入ツール: [`benchmark/soak/seed/README.md`](../soak/seed/README.md)
- 性能ベンチ全般: [`benchmark/docs/LIFECYCLE_PERF.md`](./LIFECYCLE_PERF.md)
- 設計と所見: [`docs/LIFECYCLE_ENGINE_DESIGN.md`](../../docs/LIFECYCLE_ENGINE_DESIGN.md)
