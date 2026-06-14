# JOG ライフサイクル実行エンジン 設計書

- 状態: 設計確定（実装は後続セッション）。敵対的レビュー 1 巡を反映済み
- 作成日: 2026-06-11
- 最上位制約: **COMPLIANCE 保持期限内・legal hold ON のバージョンを、いかなる経路・中断状態でも削除しない（fail-closed）**

---

## 0. 設計の要約（TL;DR）

| 論点 | 結論 |
|---|---|
| A: スキャン方式 | **案1（事前計算列なしのスキャン）をキー単位ページングで実施**。`expires_at` 列は NoncurrentDays / NewerNoncurrentVersions / EODM を表現できないため不採用 |
| B: 周期・トリガー | `time.Ticker` 既定 1h ＋ 起動 1 分後に初回実行。`jog lifecycle run [--dry-run]` サブコマンド新設。時刻は `now func() time.Time` 注入 |
| C: Object Lock | **2 層**: 計画時は `evaluateObjectLock` のコアを `internal/objectlock` に抽出して共用、実行時は**専用コネクション上の本物の `BEGIN IMMEDIATE` トランザクション内ガード**（新設 primitive。既存 metadata.go:557 の BEGIN IMMEDIATE は no-op であり流用不可 — §4） |
| D: 削除セマンティクス | Enabled: current → delete marker（データ非破壊）、noncurrent → ガード付き物理削除（**delete marker は対象外**）。EODM は「全バージョンが DM」のキーのみ掃除。Transition は対象外 |
| E: 原子性 | 新設のガード付きトランザクショナル削除（メタ削除→commit→ファイル unlink の順）。ガード判定は tx 内で行を SELECT し **Go 側で比較**（SQL 文字列比較に依存しない）。孤立ファイルは猶予付き GC。進捗チェックポイントは持たない |
| F: 並行性 | エンジンは単一 goroutine・バージョン単位の短い IMMEDIATE tx。CAS ガード（latest 不変・noncurrent 維持）で並行 PUT/DELETE と整合。`SQLITE_BUSY` は skip（fail-closed） |

**実装の前提タスク（最初に行うこと）**: `GetLatestObjectVersion`（metadata.go:1877-1883）の `ORDER BY last_modified DESC` に `version_id DESC` タイブレークを追加する。同時刻バージョンが存在すると latest 判定がノンデターミニスティックになり、current の実バージョンを noncurrent と誤判定して**消しすぎる**経路になるため、エンジン実装の前提条件とする（`GetPriorObjectVersion`:2239 には既にタイブレークがある）。

---

## 1. 論点A〜F: 推奨案と根拠

### 論点A: スキャン方式 → 案1（追加列なしスキャン、キー単位ページング）

`Expiration.Days` 以外の主要機能は**行単体の静的な期限として表現できない**。NoncurrentDays の起点は「後続バージョンの作成時刻」（noncurrent になった時点）、`NewerNoncurrentVersions` は同一キー内の相対順位、`ExpiredObjectDeleteMarker` は「同一キーの非 DM バージョンが全滅したか」という集合述語であり、案2 の `expires_at` 列はこれらを持てず、ルール変更（JSON blob 全置換）のたびに全行再計算も必要になる。案3（キュー）は単一プロセス構成では永続キューという新たな整合性問題を持ち込むだけで利点がない。SQLite のローカル走査は ~µs/行であり、日次〜時間次の周期なら数百万行でも十分間に合う。スキャン対象は `SELECT bucket FROM bucket_lifecycle` で**設定があるバケットだけ**に絞り、キーは keyset pagination（`key > ?afterKey ORDER BY key LIMIT n`、PK インデックス使用）、キーごとに全バージョンを `idx_object_versions_bucket_key`（metadata.go:202）で引いて Go 側で current/noncurrent/件数を判定する。

注意: versioning Enabled 直後の既存オブジェクトは `object_versions` 行を持たない（`snapshotNullVersionIfNeeded` filesystem.go:1597 参照）ため、キー列挙は `objects` と `object_versions` の **UNION** で行う。

### 論点B: 起動タイミング・周期・手動トリガー

サーバー内 `time.Ticker`、**既定 1h・設定可能**（`JOG_LIFECYCLE_INTERVAL`）。S3 は日次だが、期限判定そのものを「作成 + Days を翌日 0:00 UTC に切り上げ」（S3 準拠の丸め）で行うため、周期を短くしても削除タイミングの意味論は変わらず、テストと体感を改善するだけである。起動 1 分後に初回サイクルを実行する（日次間隔だと頻繁な再起動環境で一度も走らない問題を防ぐ）。手動実行は `jog lifecycle run [--bucket B] [--dry-run]` を新設する — dry-run は「何が消えるか」を出力のみ行い、有効化前の検証手段として最上位制約の運用面を支える（**dry-run はスナップショット等の副作用も一切行わない** — §3）。テスト容易性のため Engine は `now func() time.Time` をフィールドに持ち、`RunOnce(ctx)` を公開してティッカーループは薄く保つ。

エンジンの既定は **enabled=true** とする。「設定を受理して実行しない」現状こそが S3 互換性バグであり、無効デフォルトはそれを温存する。不可逆性への手当ては (1) 起動時にライフサイクル設定を持つバケット一覧を WARN ログで明示、(2) dry-run CLI、(3) `max_actions_per_cycle` 上限、(4) CHANGELOG での告知、で行う。

### 論点C: Object Lock 評価の通し方 → 案1+案2 のハイブリッド（2層防御）

**計画時（pre-filter）**: `evaluateObjectLock`（object_lock_eval.go:36-81）のコアは storage getter と time にしか依存しないため、`internal/objectlock` パッケージに `EvaluateDeletable(ctx, st storage.Storage, bucket, key, versionID string, bypassGovernance bool, now time.Time) (Verdict, error)` として抽出し、`api.Handler.evaluateObjectLock` は `*S3Error` への変換だけ行う薄いラッパーに変える。これで評価ロジックは単一実装のまま、HTTP 非依存でエンジンから呼べる（既存の objectlock テスト 26 関数はそのまま緑を維持）。エンジンは bypassGovernance=false 固定（GOVERNANCE も期限内は削除しない）。

**実行時（authority）**: pre-filter だけでは評価と削除の間に PutObjectRetention / PutObjectLegalHold が割り込む TOCTOU が残る。よって削除トランザクションの**内側**で `object_retention` / `object_legal_hold` 行を再検査するガードを置く。

ここで重要な事実: **既存コードに「機能する IMMEDIATE トランザクション」は存在しない**。metadata.go:557 / :947 の `tx.ExecContext("BEGIN IMMEDIATE")` は `BeginTx` が開いた暗黙 deferred tx の内側で発行されており、コード自身のコメント（:558-561）が nested BEGIN のエラーを握り潰す no-op だと認めている。また接続文字列（metadata.go:29）に `_txlock=immediate` はなく、コネクションプールも無制限である。よって「既存パターンの流用」ではなく、**新設 primitive `withImmediateTx`（§4）— `db.Conn(ctx)` で単一コネクションを pin し、その上で生の `BEGIN IMMEDIATE` / `COMMIT` を発行する** — を導入する。これにより、ガード SELECT と DELETE が同一コネクション・同一書き込みロック下で実行され、並行する保持設定変更は「ガードより前に commit され見える」か「`BEGIN IMMEDIATE` が `SQLITE_BUSY` になりエンジン側が skip する」かのどちらかになる（busy → skip は fail-closed）。WAL の snapshot isolation（deferred で読み→書き昇格時に `SQLITE_BUSY_SNAPSHOT` で失敗する性質）は第二の安全網であり、一次の根拠には据えない。

### 論点D: versioning 経路の削除セマンティクス

- **Enabled バケットの current Expiration → delete marker 生成**（S3 準拠・物理削除しない）。クラッシュ窓のある既存 DM 生成経路（filesystem.go:1934-1961: PutObjectVersion → DeleteObject → unlink の 3 分割）は使わず、新設のトランザクショナルメソッド `CreateExpirationDeleteMarker`（§4）を使う。current が既に DM ならスキップ。
  - **明示的な設計判断**: current が retention/legal hold 下にある場合でも DM は生成する（S3 準拠 — ロックが守るのは「バージョンのデータ」であり「current としての可視性」ではない。DM 生成後もバージョンは `GetObject(versionId)` で取得可能で、データは一切失われない）。ユーザー削除経路の `isDeleteMarkerCreation` スキップ（object.go:642）と整合する。ただし「保持中オブジェクトがリストから見えなくなる」運用上の驚きがあるため、保持中 current に DM を被せた件数をサイクル Report と INFO ログに明示し、E2E テストで「DM 生成後も versionId 指定 GET が成功する」ことを固定する。
- **NoncurrentVersionExpiration → 物理削除。ただし delete marker は対象外**。S3 の NoncurrentVersionExpiration は実バージョンの削除であり、noncurrent DM を物理削除すると S3 から乖離する上、latest 判定の揺らぎと組み合わさったときの消しすぎ方向のリスクを増やす。DM の掃除は EODM 経路に一本化する。noncurrent 起点時刻＝「直近の後続バージョンの last_modified」。同一キー内で新しい順に `NewerNoncurrentVersions` 件（非 DM のみカウント）を保護し、残りのうち noncurrent 経過日数が NoncurrentDays 以上の**非 DM バージョン**をガード付き削除（§4 `ExpireObjectVersionGuarded`）にかける。`DeleteObjectVersioned`(filesystem.go:1834) をそのまま流用しない理由は、非トランザクション・ファイル先行削除のため大量削除の中断耐性が不足するから。noncurrent 限定なら latest 再構築も不要で、新メソッドは小さく済む。
- **ExpiredObjectDeleteMarker → 「キーの全バージョンが DM」のときのみ全 DM 行を削除**。これが S3 の expired object delete marker の定義（配下に実バージョンが残っていない DM）であり、誤って latest DM を消して旧バージョンを「復活」させる事故をトランザクション内ガード（非 DM 行が 0 件であること）で構造的に防ぐ。`Expiration.Days` による自動 EODM 掃除（S3 の付随挙動）は v1 では行わず、明示的な `ExpiredObjectDeleteMarker=true` のみ実行する（保守的側）。
- **Suspended バケットの Expiration は v1 ではスキップ（WARN ログ）**。S3 の suspended 意味論（null バージョンを null DM で置換＝データ破壊）と JOG の現行ユーザー削除（current 物理削除、ただし `object_versions` の '' 行が残る既存の不整合 — §7-1）が一致しておらず、ここで独自判断するより「何も消さない」が最上位制約に忠実。NoncurrentVersionExpiration / EODM / AIMU は Suspended でも意味論が同じなので実行する。
- **Transition / NoncurrentVersionTransition は対象外（no-op + debug ログ）**。単一ノード・ローカル FS にストレージクラスの実体がなく、オブジェクトごとの StorageClass すら永続化されていない。CRUD が受理するのは互換性のためで、実行は意味を持たない。
- **AbortIncompleteMultipartUpload**: `multipart_uploads.initiated`（metadata.go:101）+ DaysAfterInitiation（S3 同様の UTC 丸め）が経過したアップロードを、ルールの Filter.Prefix と突き合わせて `fs.AbortMultipartUpload`（filesystem.go:1222）で中止する。S3 仕様によりタグフィルタ付きルールでは AIMU を適用しない。対象は**先に全ページを列挙して確定してから**実行する（abort しながらのページングはマーカーがずれるため）。Abort は「parts ディレクトリ RemoveAll → メタ削除」の順で冪等（クラッシュしても次サイクルで再試行可能）なので既存実装をそのまま流用する。

### 論点E: 削除の原子性・クラッシュ安全性

方針: **メタデータ削除を 1 トランザクションに集約 → commit → ファイル unlink** の順に統一する（既存 `DeleteObject` filesystem.go:337 のファイル先行と逆）。この順序だと、クラッシュ時に起きうる不整合は「行が無いのにファイルが残る」（不可視・無害・GC 可能）だけになり、「行があるのにファイルが無い」（ユーザー可視の 500）を構造的に排除できる。トランザクション内容はバージョン行 + lock 行 + acl/tag 行の削除（既存 `DeleteObjectLockRows`:2379 / `DeleteObjectACLTagRows`:2396 の SQL を tx 内に取り込む）＋論点C/F のガード。**ガード判定は tx 内で対象キーの行を SELECT し、Go 側の `time.Time` 比較で行う**（SQL の DATETIME 文字列比較に依存しない — タイムゾーン表記の混在で字句比較が破綻するリスクを根元から断つ。バインドする時刻は常に `.UTC()` 正規化）。`os.Remove` は ENOENT を無視して冪等。

進捗チェックポイントは**持たない**。全操作が冪等（削除済み行の再削除は no-op、ガードは状態変化を検知して skip）なので、クラッシュ後はサイクル先頭からの再スキャンで正しく回復する。ローカル SQLite の再スキャンコストは低く、チェックポイントテーブルの整合性管理という新たな複雑性に見合わない。代わりに `max_actions_per_cycle` で 1 サイクルの作業量を上限し、残件は次サイクルが拾う。

孤立ファイル GC（v1 スコープを限定）: サイクル末尾（または低頻度の別フェーズ）で対象を **(1) `.versions/<key>/<versionID>` のうち `object_versions` 行が無いファイル、(2) `.uploads/<uploadID>` のうち `multipart_uploads` 行が無いディレクトリ、(3) `.tmp-*`** に限り、いずれも **mtime が猶予期間（1h）より古いもの**だけ削除する。実装は「行が参照する全パスの集合を先に構築 → ファイル走査との差集合」とし、列挙漏れ＝消しすぎを防ぐ。猶予は `PutObjectVersioned`（filesystem.go:1716-1738）の「rename → 行 INSERT」順序と書き込み中ファイルの混同を防ぐために必須。**current ファイル（`dataDir/bucket/key`）は v1 では GC 対象外**（`rebuildCurrentAfterVersionDelete`:2576 等の書き込み順序前提を検証するまで触らない）。

### 論点F: 並行性

エンジンは**単一 goroutine・直列実行**とする。SQLite は単一ライターであり、削除はメタ操作＋unlink の軽量 I/O なので並列化は競合を増やすだけで得るものがない。ロック粒度は「バージョン 1 件 = 1 つの短い IMMEDIATE tx」。サイクル全体やバケット全体を 1 トランザクションで包むことは**しない**（リクエストパスの書き込みを長時間ブロックするため）。ユーザー書き込みとの衝突は WAL + `busy_timeout=5000`（metadata.go:29）が吸収し、`BEGIN IMMEDIATE` が busy になった場合は**そのバージョンを skip して次へ進む**（fail-closed: 削除しない方向に倒れる）。スループット制御として N 操作ごとに固定スリープ（既定: 100 操作ごとに 10ms）を挟む。

計画の陳腐化（スキャン中の並行 PUT/DELETE）はトランザクション内 CAS ガードで解決する:
- DM 生成: 「latest が計画時のバージョン ID のまま、かつ非 DM」を tx 内で検証（並行 PUT で新 version ができていたら skip）
- noncurrent 物理削除: 「自分より新しい行が存在する＝今も noncurrent」を tx 内で検証（並行の latest 削除で current に昇格していたら skip）
- EODM: 「非 DM 行が 0 件」を tx 内で検証

これらの検証はすべて「tx 内で対象キーの全バージョン行（少量）を 1 回 SELECT → Go 側で判定」する形で実装し、§0 のタイブレーク修正後の順序規約 `(last_modified DESC, version_id DESC)` を全箇所で共有する。skip は失敗ではなく「次サイクルで再評価」を意味する。ルール変更はサイクル先頭のスナップショットで読み、サイクル途中の変更は次サイクルから反映（S3 の非同期実行と同等の意味論）。

---

## 2. アーキテクチャ

```
internal/lifecycle/            ← 新規パッケージ
  engine.go      Engine struct / Run(ctx) ティッカーループ / RunOnce(ctx) 1サイクル
  evaluate.go    ルール評価の純粋関数群（時刻丸め・フィルタ・noncurrent計算・EODM判定）
  actions.go     Action 型（ExpireCurrentDM / ExpireNoncurrent / CleanupEODM / AbortMPU / ExpireCurrentPhysical）
internal/objectlock/           ← 新規パッケージ（evaluateObjectLock のコア抽出先）
  evaluate.go    EvaluateDeletable(ctx, st, bucket, key, versionID, bypassGovernance, now)
```

```go
type Engine struct {
    storage storage.Storage
    cfg     Config            // Interval, MaxActionsPerCycle, ThrottleEvery/ThrottleSleep, DryRun
    now     func() time.Time  // テストで注入
    log     zerolog.Logger
}

func (e *Engine) Run(ctx context.Context)              // ticker ループ。ctx.Done() で即終了
func (e *Engine) RunOnce(ctx context.Context) (Report, error)  // テスト可能な単位
```

**server.go への統合点**（internal/server/server.go）:

```go
type Server struct {
    httpServer *http.Server
    storage    storage.Storage
    config     *config.Config
    lifecycle  *lifecycle.Engine   // 追加
    lcCancel   context.CancelFunc  // 追加
    lcDone     chan struct{}       // 追加
}
```

- `New()`（server.go:25）: storage 生成後に `lifecycle.NewEngine(store, cfg.Lifecycle, time.Now)` を生成
- `Start()`（server.go:60）: `cfg.Lifecycle.Enabled` のとき goroutine で `engine.Run(lcCtx)` を起動してから `ListenAndServe()`。起動時にライフサイクル設定を持つバケット一覧を WARN で出力
- `Shutdown()`（server.go:70）: **`lcCancel()` → `<-lcDone`（エンジン停止待ち）→ `httpServer.Shutdown` → `storage.Close()`** の順。エンジンはバージョン単位の境界で ctx を確認するため停止待ちは最長でも 1 トランザクション分
- `internal/cli/server.go:100` の起動 goroutine は変更不要（Server 内部に閉じる）

**CLI**: `internal/cli/lifecycle.go` 新設 — `jog lifecycle run [--config|--data-dir] [--bucket B] [--dry-run]`。config を読み、storage を直接開いて `RunOnce` を 1 回実行し、Report（バケット別: 評価数 / 削除数 / skippedLocked / skippedBusy / エラー）を表示する。WAL なので稼働中サーバーの DB に対しても安全（ガードと冪等性が二重実行を無害化する）が、通常はサーバー内蔵ティッカーで足りる旨をヘルプに明記。

**Config 追加**（internal/config/config.go:15）:

```yaml
lifecycle:
  enabled: true              # JOG_LIFECYCLE_ENABLED
  interval: 1h               # JOG_LIFECYCLE_INTERVAL (Go duration)
  max_actions_per_cycle: 10000   # JOG_LIFECYCLE_MAX_ACTIONS
```

---

## 3. 処理フロー（擬似コード）

```
RunOnce(ctx):
  report = {}
  for bucket in storage.ListBuckets():                        # 設定の無いバケットは即 continue
    rules = storage.GetBucketLifecycleConfiguration(bucket)   # ErrNoSuchLifecycle → continue
    versioning, err = storage.GetBucketVersioning(bucket)
    if err != nil: log.Error; continue                        # fail-closed: 不明状態のバケットは触らない (#48 と同方針)

    # --- AbortIncompleteMultipartUpload ---
    for rule in enabledRules(rules) where rule.AbortIncompleteMultipartUpload != nil:
      if rule.Filter.Tag != nil: continue                     # S3: タグフィルタと AIMU は併用不可
      targets = collectAll(storage.ListMultipartUploads(bucket))  # 先に全列挙してから実行（マーカーずれ防止）
      for upload in targets:
        if hasPrefix(upload.Key, rule.Filter.Prefix)
           and now >= roundUpMidnightUTC(upload.Initiated) + DaysAfterInitiation:
          act(AbortMPU): storage.AbortMultipartUpload(...)    # 冪等・既存実装流用

    # --- オブジェクトスキャン ---
    if versioning == Disabled:
      for obj in pageAll(storage.ListObjectsV2(bucket)):      # objects テーブルのみ
        rule = matchExpiration(rules, obj)                    # prefix/tag/size フィルタ + Days/Date 判定
        if rule == nil: continue
        if deny = objectlock.EvaluateDeletable(bucket, key, "", false, now); deny: skip++  # pre-filter
        else: act(ExpireCurrentPhysical):
          storage.ExpireCurrentObjectGuarded(bucket, key, obj.LastModified, now)   # §4 (tx 内ガード)

    else:  # Enabled / Suspended
      for key in pageKeys(storage.ListLifecycleObjectKeys(bucket, after)):  # objects ∪ object_versions
        vs = storage.GetObjectVersionsForKey(bucket, key)     # (last_modified DESC, version_id DESC)
        current = vs[0]; noncurrent = vs[1:]

        # (1) Expiration → delete marker（Enabled のみ。Suspended は skip + WARN）
        if versioning == Enabled and rule = matchExpiration(rules, current);
           rule != nil and !current.IsDeleteMarker:
          # pre-versioning オブジェクト（objects 行のみ・version 行なし）はここで初めて副作用を実行する
          # （スキャン中ではなくアクションとして。dry-run では実行しない）
          act(ExpireCurrentDM):
            if vs == [] and objectsRowExists:
              snapshotNullVersionIfNeeded(bucket, key)        # 冪等。current を '' 版として退避
              expected = ""
            else: expected = current.VersionID
            storage.CreateExpirationDeleteMarker(bucket, key, expected)  # tx 内 CAS
            # 注: current が retention/legal hold 下でも DM は生成する（S3 準拠・データ非破壊、
            #     バージョンは versionId 指定で取得可能なまま）。件数を Report に計上し INFO ログ。

        # (2) NoncurrentVersionExpiration → 物理削除（delete marker は対象外）
        for rule in enabledRules where rule.NoncurrentVersionExpiration != nil and filterMatch(rule, key):
          keep = rule.NewerNoncurrentVersions ?? 0
          realNoncurrent = [v for v in noncurrent if !v.IsDeleteMarker]   # DM 除外（S3 準拠・消しすぎ防止）
          for i, v in realNoncurrent:                         # 新しい順
            if i < keep: continue
            noncurrentSince = successorOf(v, vs).LastModified # 直近の後続バージョンの作成時刻
            if now < roundUpMidnightUTC(noncurrentSince) + NoncurrentDays: continue
            if deny = objectlock.EvaluateDeletable(bucket, key, v.VersionID, false, now); deny: skip++; continue
            act(ExpireNoncurrent):
              storage.ExpireObjectVersionGuarded(bucket, key, v.VersionID,
                  guards={DenyIfLocked, RequireNoncurrent}, now)

        # (3) ExpiredObjectDeleteMarker → 全バージョンが DM のキーのみ掃除
        if ruleHasEODM(rules, key) and vs != [] and all(v.IsDeleteMarker for v in vs):
          for v in vs:
            act(CleanupEODM):
              storage.ExpireObjectVersionGuarded(bucket, key, v.VersionID,
                  guards={DenyIfLocked, RequireAllVersionsAreDeleteMarkers}, now)

        if report.actions >= cfg.MaxActionsPerCycle: return report   # 残件は次サイクル
        throttle(); if ctx.Done(): return report

  orphanFileGC(graceperiod=1h)                                # §1-E のスコープ限定 GC（dry-run では実行しない）
  return report

act(a): if cfg.DryRun { report.plan(a) } else { execute(a); report.count(a) }
```

期限丸めヘルパ（S3 準拠・全アクション共通）:

```
roundUpMidnightUTC(t) = 00:00 UTC of (t.UTC().Date() + 1day)
eligible(now, created, days) = now >= roundUpMidnightUTC(created.AddDate(0,0,days))
# Date 指定は RFC3339 をパースし now >= date。パース不能はルールを skip + ERROR ログ（fail-safe: 消さない）
```

---

## 4. スキーマ変更 / storage インターフェース変更

### 4.1 新設 primitive: `withImmediateTx`（最重要）

既存コードには機能する IMMEDIATE トランザクションが**存在しない**（metadata.go:557/:947 は BeginTx の暗黙 deferred tx 内の nested BEGIN で、エラーを握り潰す no-op — コメント :558-561 が自認。DSN にも `_txlock` なし）。ガード付き削除の排他はこの新設ヘルパで実現する:

```go
// withImmediateTx pins a single pooled connection and runs fn inside a real
// BEGIN IMMEDIATE transaction. BEGIN IMMEDIATE acquires the write lock up
// front, so every read fn performs sees the latest committed state and no
// concurrent writer can interleave before COMMIT.
// SQLITE_BUSY at BEGIN (after busy_timeout) returns ErrBusy — callers treat
// it as fail-closed skip, never as "proceed without guard".
func (m *Metadata) withImmediateTx(ctx context.Context, fn func(conn *sql.Conn) error) error {
    conn, err := m.db.Conn(ctx)         // database/sql: 以後の Exec/Query はこの conn に固定される
    if err != nil { return err }
    defer conn.Close()
    if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil { return wrapBusy(err) }
    if err := fn(conn); err != nil {
        _, _ = conn.ExecContext(ctx, "ROLLBACK")
        return err
    }
    _, err = conn.ExecContext(ctx, "COMMIT")
    return err
}
```

実装時の検証項目: modernc.org/sqlite で生の `BEGIN IMMEDIATE` / `COMMIT` が `*sql.Conn` 上で機能すること（並行 writer との競合テストで `SQLITE_BUSY` → skip を実測する）。将来的に metadata.go:557/:947 の no-op をこのヘルパに置き換える改善は別 Issue（§7-6）。

### 4.2 スキーマ変更

**既存テーブルの ALTER は不要**（インデックスも `idx_object_versions_bucket_key` metadata.go:202 と PK で足りる）。追加は観測用の 1 テーブルのみ（任意・additive なので `initialize()` の `CREATE TABLE IF NOT EXISTS` パターンに従い、`PRAGMA user_version` の繰り上げは不要 — user_version は破壊的移行専用という既存規約 metadata.go:316-318 を維持）:

```sql
CREATE TABLE IF NOT EXISTS lifecycle_runs (
    bucket TEXT PRIMARY KEY,
    last_run_at DATETIME NOT NULL,
    actions INTEGER NOT NULL DEFAULT 0,
    skipped_locked INTEGER NOT NULL DEFAULT 0,
    errors INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
)
```

### 4.3 Storage インターフェース追加（internal/storage/interface.go:464 の `Storage` に追記）

```go
// --- Lifecycle engine support ---

// ListLifecycleObjectKeys はバケット内のオブジェクトキーを keyset pagination で返す。
// objects ∪ object_versions の DISTINCT（pre-versioning オブジェクトを漏らさないため）。
ListLifecycleObjectKeys(ctx context.Context, bucket, afterKey string, limit int) ([]string, error)

// GetObjectVersionsForKey は 1 キーの全バージョンを (last_modified DESC, version_id DESC) で返す。
GetObjectVersionsForKey(ctx context.Context, bucket, key string) ([]ObjectVersion, error)

// ExpireObjectVersionGuarded は withImmediateTx 内で「ロックガード → 状態ガード →
// バージョン行 + lock/acl/tag 行の削除」を行い、commit 後にバージョンファイルを unlink する。
// ガード不成立はエラーではなく Outcome (SkippedLocked / SkippedStateChanged / SkippedBusy) で返す。
ExpireObjectVersionGuarded(ctx context.Context, bucket, key, versionID string,
    guards ExpireGuards, now time.Time) (ExpireOutcome, error)

// CreateExpirationDeleteMarker は withImmediateTx 内で「latest == expectedCurrentVersionID
// かつ非 DM」を CAS 検証し、DM 行 INSERT + objects 行 DELETE を行い、commit 後に
// current ファイルを unlink する。
CreateExpirationDeleteMarker(ctx context.Context, bucket, key,
    expectedCurrentVersionID string) (markerVersionID string, outcome ExpireOutcome, err error)

// ExpireCurrentObjectGuarded は非バージョニングバケット用。withImmediateTx 内で
// 「'' 版ロックガード → objects.last_modified == expected 検証 → objects 行 +
// '' 版 lock/acl/tag 行の削除」を行い、commit 後に current ファイルを unlink する。
ExpireCurrentObjectGuarded(ctx context.Context, bucket, key string,
    expectedLastModified time.Time, now time.Time) (ExpireOutcome, error)
```

```go
type ExpireGuards struct {
    RequireNoncurrent              bool // 自分より新しい行が存在すること
    RequireAllVersionsAreDeleteMarkers bool // キーの非 DM 行が 0 件であること
}   // ロックガード（retention/legal_hold）は常時有効でオプション化しない

type ExpireOutcome int // Expired / SkippedLocked / SkippedStateChanged / SkippedBusy / NotFound
```

### 4.4 `ExpireObjectVersionGuarded` のトランザクション内容（実装規範）

```
withImmediateTx:
  # --- 読み取りはすべて SELECT → Go 側で比較（SQL の DATETIME 文字列比較を使わない） ---
  hold   := SELECT status FROM object_legal_hold WHERE bucket=? AND key=? AND version_id=?
  if hold == "ON"                          → ROLLBACK, SkippedLocked
  ret    := SELECT mode, retain_until_date FROM object_retention WHERE ...
  if ret != nil && ret.retainUntil.After(now.UTC())   # モード不問: GOVERNANCE もエンジンは bypass しない
                                           → ROLLBACK, SkippedLocked
  vs     := SELECT version_id, last_modified, is_delete_marker FROM object_versions
            WHERE bucket=? AND key=?       # 1 キー分は少量。順序判定は Go 側で
                                           #  (last_modified DESC, version_id DESC) に統一
  if guards.RequireNoncurrent && !existsNewerThan(vs, target)   → ROLLBACK, SkippedStateChanged
  if guards.RequireAllVersionsAreDeleteMarkers && anyRealVersion(vs) → ROLLBACK, SkippedStateChanged
  DELETE FROM object_versions  WHERE bucket=? AND key=? AND version_id=?   # 0行なら NotFound
  DELETE FROM object_retention WHERE ... / object_legal_hold / object_acls / object_tags  # 後始末
  COMMIT
# commit 成功後: os.Remove(versionFilePath)  (ENOENT は無視 = 冪等)
```

`CreateExpirationDeleteMarker` も同形: tx 内で vs を SELECT → Go 側で「latest（タイブレーク込み）== expected かつ非 DM」を CAS 検証 → DM 行 INSERT + objects 行 DELETE → COMMIT → current ファイル unlink。

### 4.5 既存コードの変更

| 順序 | 対象 | 変更 |
|---|---|---|
| 1（前提） | internal/storage/metadata.go:1877-1883 | `GetLatestObjectVersion` の ORDER BY に `version_id DESC` タイブレーク追加（§0。エンジンの latest/noncurrent 判定と CAS ガードはすべてこの順序規約に依存するため最初に実装・テストする） |
| 2 | internal/storage/metadata.go | `withImmediateTx` 新設（§4.1） |
| 3 | internal/api/object_lock_eval.go:36 | コアロジックを `internal/objectlock.EvaluateDeletable` へ移し、`evaluateObjectLock` は Verdict→`*S3Error` 変換ラッパーに（挙動・テスト不変） |
| 4 | internal/storage/ | §4.3 の 5 メソッド実装 |
| 5 | internal/config/config.go | `LifecycleConfig` 追加 + env オーバーレイ（applyEnv:129） |
| 6 | internal/lifecycle/ + internal/server/server.go | エンジン本体と統合（§2） |
| 7 | internal/cli/ | `lifecycle.go` サブコマンド新設 |

`DeleteObjectVersioned`（filesystem.go:1834）と `DeleteObject`（:337）の**ユーザー経路は本件では変更しない**（スコープ抑制）。将来 tx メソッドへ寄せる改善余地は §7 に記録する。

---

## 5. 原子性・クラッシュ安全性の不変条件

エンジンの全操作が任意の点で中断されても、以下が常に成り立つ:

- **I1（保護の絶対性）**: COMPLIANCE/GOVERNANCE 期限内・legal hold ON のバージョン行とそのファイルは、エンジンによって削除されない。根拠: 削除はガード付き tx 経由のみで、ガード SELECT と DELETE は `withImmediateTx` により同一コネクション・同一書き込みロック下にある（§4.1）。並行する保持設定変更は「ガード前に commit され見える」か「BEGIN が busy になりエンジンが skip する」のいずれか。pre-filter／tx ガードとも未知エラー・busy はすべて skip（fail-closed）。
- **I2（行 ⇒ ファイル）**: エンジンの操作は「`object_versions` に非 DM 行が存在するならファイルも存在する」を破らない。根拠: エンジンの削除は「行削除 commit → unlink」の順なので、中断は「行なし・ファイルあり」（孤立ファイル）しか生まない。※ユーザー経路（filesystem.go:1910→1914 のファイル先行）のクラッシュは逆向きの dangling 行を残しうるが、これは既存問題でありエンジンは悪化させない（§7-1）。エンジンがそのような行に出会った場合、unlink の ENOENT 無視により正常に掃除できる。
- **I3（冪等性）**: 同一サイクルの再実行・多重実行（サーバー内蔵 + CLI 併走を含む）は安全。根拠: 行削除の重複は no-op（NotFound）、`os.Remove` は ENOENT 無視、DM 生成は CAS（expected latest 不一致なら skip）なので二重 DM は生じない、AbortMPU は RemoveAll + メタ削除とも冪等。
- **I4（部分削除なし）**: 1 バージョンのメタデータ（version/lock/acl/tag 行）は全削除か無削除のどちらか。根拠: 単一トランザクション。
- **I5（孤立ファイルの収束）**: I2 で生じた孤立ファイルは、猶予 1h 経過後の GC フェーズで回収される。GC は「行が参照する全パス集合との差集合」かつ `.versions/` `.uploads/` `.tmp-*` 限定・mtime 猶予付き（§1-E）で、正規ファイルを誤回収しない。
- **I6（進捗非依存）**: エンジンは永続的な進捗状態を持たず、毎サイクル現在の DB 状態だけから判断する。クラッシュ後の回復手順は「次サイクルを待つ」のみ。

---

## 6. テスト戦略（TDD）

実装順序は CLAUDE.md の Red-Green-Refactor に従い、**タイブレーク修正 → withImmediateTx → storage ガードメソッド → 評価ロジック → エンジン統合 → s3compat E2E** の順で各層 Red から始める。

### (a) internal/storage: 基盤のテスト（新規）

- `GetLatestObjectVersion` タイブレーク regression: 同一 `last_modified` の複数バージョンで latest が `version_id DESC` で決定的になること
- `withImmediateTx`: 並行 writer（別 goroutine が `BEGIN IMMEDIATE` 保持中）で `SQLITE_BUSY` が返り skip 扱いになること。tx 内の SELECT が直前 commit 済みの retention 行を必ず見ること（**並行「retention 設定 → expire」競合を実際に走らせる race テスト**）

### (b) internal/storage: ガード付き削除メソッドのテスト（新規 lifecycle_expire_test.go）

- **regression（最重要・最上位制約の直接検証）**:
  - COMPLIANCE + retain_until_date 未来 → `ExpireObjectVersionGuarded` が SkippedLocked、行・ファイル・GET とも無傷
  - legal hold ON（retention なし）→ 同上
  - GOVERNANCE + 未来 → 同上（エンジンに bypass なし）
  - retention 期限切れ → Expired になり行+lock+acl+tag 行が消えファイルも消える
  - retain_until_date のタイムゾーン表記が混在しても（`+09:00` 書き込み等）判定が正しいこと（Go 側比較 + UTC 正規化の固定）
- 状態ガード: RequireNoncurrent で対象が latest に昇格済み → SkippedStateChanged / RequireAllDM で実バージョン残存 → SkippedStateChanged
- CAS: `CreateExpirationDeleteMarker` で expected と異なる latest → skip、二重呼び出しで DM が 1 個のみ
- クラッシュ模擬: 行削除 commit 後にファイルが残った状態を人工的に作り、再実行が NotFound（無害）になること、GC が猶予内ファイル・行参照のあるファイルを消さず、猶予超過の孤立ファイルだけ消すこと

### (c) internal/lifecycle: 評価ロジックの純粋関数ユニットテスト（新規）

時刻注入のテーブル駆動で: UTC 翌日 0:00 丸め（境界値: ちょうど Days 経過直前/直後・日付またぎ）、Date ルール、フィルタ（prefix / tag / ObjectSize範囲 / Filter なし＝全件）、Status=Disabled の無視、noncurrentSince の導出（後続バージョン時刻）、NewerNoncurrentVersions の保護件数（非 DM のみカウント）、**noncurrent DM が削除対象に入らないこと**、EODM 判定（全 DM / 実バージョン混在）、不正 Date のルール skip。

### (d) test/s3compat: E2E（新規 lifecycle_engine_test.go。既存 lifecycle_test.go は CRUD 用のまま不変）

`testutil.NewTestServer` を拡張してエンジンへの参照（`RunOnce` + now 注入フック）を公開し、AWS SDK で状態を作り「未来の now」で 1 サイクル回して SDK で観測する:

- Expiration: versioned バケットで DM が生成され GET が 404 / ListObjectVersions に DeleteMarker が現れ実バージョンは残存
- 非バージョニングバケットで物理削除
- NoncurrentVersionExpiration: 古い noncurrent のみ消え、NewerNoncurrentVersions 件が保護され、current と noncurrent DM は無傷
- ExpiredObjectDeleteMarker: 実バージョン全削除後の DM だけのキーが Listing から消える
- AbortIncompleteMultipartUpload: 期限超過アップロードのみ中止（ListMultipartUploads / ListParts で確認）
- **Object Lock regression**: COMPLIANCE 期限内 / legal hold ON のバージョンはサイクル後も GetObject(versionId) で取得可能（既存 objectlock_test.go のヘルパー流用）。**保持中 current に Expiration DM が被った後も versionId 指定 GET が成功すること**（論点D の設計判断の固定）
- 冪等性: 同一サイクル 2 連続実行で結果不変
- dry-run: Report に計画が載り、データ・スナップショット・GC とも一切変化しない
- Suspended バケット: Expiration が skip され何も消えない

### (e) 既存テスト

objectlock_test.go（26 関数）・versioning_test.go（9 関数）・internal/api/object_lock_headers_test.go は **無変更で緑を維持**することが evaluateObjectLock 抽出リファクタの完了条件。

---

## 7. 追加リスク・申し送り

1. **suspended バケットの既存不整合**: ユーザー経路の unspecified delete（→ `DeleteObject`:337）は `object_versions` の '' null 版行を残したまま current ファイルを消すため、'' 版が dangling になる（エンジン以前からの問題）。同根で、`DeleteObjectVersioned`（:1910→:1914）のファイル先行削除はクラッシュ時に「行あり・ファイルなし」を残す。エンジンは Suspended の Expiration を skip し ENOENT を無視するため悪化させないが、別 Issue として起票推奨。
2. **時計の前進ジャンプ**: ホスト時計が大きく進むと早期削除が起きる（S3 等価のリスク）。retention ガードも同じ時計で評価されるため保護不変条件は破れない。
3. **EODM の自動掃除（Expiration.Days 併用時の S3 付随挙動）**は v1 対象外。なお `ExpiredObjectDeleteMarker` と `Expiration.Days`/`Date` の同時指定を PUT で拒否する S3 互換バリデーション（InvalidRequest, HTTP 400）は #55 で対応済み（`internal/api/lifecycle.go` の `validateLifecycleConfiguration`）。
4. **通知イベント**: S3 は lifecycle 削除で `LifecycleExpiration:*` イベントを発行する。JOG は Webhook（HTTP POST）配信を実装済み（#56）。エンジンは `Notifier` インターフェース経由で発火点にフックし、実削除（`ExpireExpired`）時のみ `LifecycleExpiration:Delete`（非バージョン削除 / 永続版削除）と `LifecycleExpiration:DeleteMarkerCreated`（バージョン管理 current の期限切れ）を発行する。AbortIncompleteMultipartUpload / EODM 掃除には対応イベントが存在しないため何も発行しない。dry-run では発行しない。SNS/SQS/Lambda/EventBridge 配信は別 Issue。
5. **Litestream 連携**（docs/DEPLOYMENT.md）: 大量削除は WAL を膨らませる。`max_actions_per_cycle` が実質の上限となり replication への影響を抑える。
6. **metadata.go:557/:947 の no-op `BEGIN IMMEDIATE`**: 移行コードが意図した排他が実際には取れていない（単一プロセスでは BeginTx の分離で実害なし）。`withImmediateTx` 安定後に置き換える改善を別 Issue で。
7. **ユーザー削除経路の原子性**: `DeleteObjectVersioned` / `DeleteObject` のファイル先行・非 tx は本設計のスコープ外として残置。エンジン用 tx メソッドが安定したら user 経路を同メソッドへ寄せる改善を別 Issue で。
8. **Filter の And 未対応**: S3 の `<Filter><And>` 複合は CRUD 構造体ごと未対応（フラットな Prefix+Tag+Size を AND 解釈）。エンジンは保存形式に従うのみで、互換ギャップとして記録。
9. **保持中 current への Expiration DM**（論点D の設計判断）: S3 準拠で生成する（データ非破壊・versionId で取得可能）が、「保持中なのにリストから消える」を驚きと感じる運用者向けに Report/INFO ログで件数を可視化する。問題になれば「保持中 current には DM を被せない」オプション追加で対応可能な構造にしておく。

---

## 8. 実装後レビュー (codex) の指摘と対応

PR #51 の codex レビュー結果。最上位制約（Object Lock fail-closed）・`withImmediateTx` の排他性・トランザクション原子性・CAS ガード・孤立ファイル GC には指摘なし。

- **[P1] NCVE が Tag/Size フィルタを無視 → 修正済み**: `noncurrentRule` は prefix のみ判定していたため、Tag/Size 付きルールで非該当の noncurrent バージョンまで削除しうる過剰削除バグだった。`noncurrentExpiryCandidates` に match 述語を追加し、エンジン側で各 noncurrent バージョンに対し size + バージョン別タグでフィルタを評価してから削除するよう修正。E2E（タグ付与前後の挙動）で固定。
- **[P2/2巡目] `NewerNoncurrentVersions` の保護件数は全実非現行版で数える → 修正済み**: 上記 P1 修正時に保持件数カウンタを「フィルタ一致版のみ」で進めてしまっていたが、S3 仕様では保持件数は**フィルタに関係なく全ての新しい非DM非現行版**を数える（一致版の削除可否は「自分より新しい非現行版が keep を超えるか」で決まる）。フィルタは削除候補の選別のみに使い、保持カウンタは全実非現行版で進めるよう修正（非該当版は keep スロットを占有するが削除はされない＝P1 は維持）。Object Lock 保護とは無関係（ストレージ層ガードが担保）。`keep-counts-nonmatching-versions` 等で固定。
- **[P1] `NewerNoncurrentVersions` 単独で `NoncurrentDays` なし → 意図的挙動として維持**: S3 仕様上 `NewerNoncurrentVersions` 単独でも超過分は失効対象。本実装は `days=0`（翌日 0:00 丸めにより約 1 日の猶予あり）で S3 準拠。`NoncurrentDays`・`NewerNoncurrentVersions` の双方が無いルールのみ skip（不完全ルール）。
- **[P2] 複数 AIMU / NCVE ルール → v1 既知の制限（過少削除＝安全側）**: 同一キー/アップロードに複数ルールが重なる場合、現状は prefix 一致の最初のルールのみ適用（マージしない）。安全側に倒れるため v1 では許容し、複数ルールマージは別 Issue 候補。`noncurrentRule` / `abortMPURule` のコメントに明記。
- **[P1/公開前レビュー] current ファイル unlink と並行 PUT の TOCTOU → 修正済み**: エンジンの `CreateExpirationDeleteMarker` / `ExpireCurrentObjectGuarded` は commit 後に `currentPath`（`dataDir/bucket/key`）を unlink するが、これは PUT も書き込む共有パス。commit〜unlink の間に同一キーへの PUT が完了すると、成功した PUT の current ファイルを消す（**非バージョニングはデータ損失**、バージョニングは `.versions` にデータが残り current GET が壊れる可用性バグ）。`FileSystem` に (bucket,key) 単位の striped mutex（`keyMu`・1024 stripe）を追加し、PUT 経路（`PutObject`/`PutObjectVersioned` の current 発行部）とエンジンの 2 メソッドで共有取得して直列化。ロック順は常に keyMu → SQLite（`withImmediateTx`）でデッドロック無し。遅い body コピーはロック外。これにより §5 の不変条件 I2（行 ⇒ ファイル）がエンジン経路で厳密に成立する。`TestExpireCurrent_NoRaceWithConcurrentPut`（-race・100反復の並行 PUT vs expire）で固定。noncurrent 版 unlink（`.versions/key/<uuid>`、ユニークパス）はこの競合の対象外。ユーザー削除経路（§7-1/§7-7）の同種競合は引き続きスコープ外。

---

## 9. ベンチマークと WAL/busy_timeout 修正（実装後調査）

PR #51 の性能影響を in-process Go ベンチで測定（Apple M2）。

- ガード付き削除 `ExpireObjectVersionGuarded`（455µs/op）は既存の非ガード削除 `DeleteObjectVersioned`（505µs/op）より高速。`withImmediateTx` の固定オーバーヘッドは約 8µs/op。エンジンのアイドル時オーバーヘッドは ticker のみ（ほぼゼロ）。性能リグレッションなし。
- `RunOnce` のスキャンは 2000 キー（4000 バージョン行・該当なし）で約 167ms（≒83µs/キー）。毎時実行の定常コスト。
- **重大な既存バグを発見・修正**: SQLite DSN の `_journal_mode=WAL` / `_busy_timeout=5000` が modernc では無視されており（実測 `journal_mode=delete`・`busy_timeout=0`）、本設計が前提とする「WAL snapshot isolation」「busy_timeout が競合を吸収」がいずれも成立していなかった。`_pragma=journal_mode(WAL)` / `_pragma=busy_timeout(5000)` に修正。エンジン用コネクションは `withImmediateTx` 内で busy_timeout(100ms) に絞り、競合時に速やかに skip（fail-closed）を維持。修正後のベンチでエンジン競合下の Put が 22.8ms→4.3ms、BUSY リトライ 13.5/op→0/op に改善。Litestream（WAL 必須）も実際に機能するようになった。
- **スキャンのスケーリング検証（DB一括seed）で O(n²) バグを発見・修正**: `ListLifecycleObjectKeys` の `objects UNION object_versions` ページングが各ブランチに `LIMIT` を押し下げておらず、100万キーで 384s（超線形）。各ブランチに `ORDER BY key LIMIT`（object_versions は `DISTINCT key`・version-only キー保護）を押し下げ、100万キー 21.6s・per-key ほぼ一定の線形に改善（約18倍）。`BenchmarkRunOnce_ScanScale` と `TestListLifecycleObjectKeys_VersionOnlyPagination` で固定。
- **タグ取得の最適化（実施済み）**: スキャン時、タグフィルタを持つ Expiration ルールが無ければ current オブジェクトのタグ取得をスキップ（`needTags`）。スキャン 167ms→86ms/2000キー。`benchmark/docs/LIFECYCLE_PERF.md` に計測手順を集約。
