# soak seed — ライフサイクルソーク用データ投入ツール

## 概要

JOG のデータディレクトリに **backdate（過去日付）した大量のオブジェクトバージョン**を一括投入する Go ツール。メタデータ DB に行を直接 INSERT する（データファイルは作らない — エンジンの noncurrent 削除は `unlink` の ENOENT を無視するため不要）。100 万キーでも**数秒**で投入でき、実 S3 PUT（~4ms/個 ＝ 100 万で ~1 時間）を避けられる。

タイムスタンプを過去にずらすため、投入された noncurrent バージョンは既に `NoncurrentDays` を超過しており、エンジンが走った瞬間に削除対象になる。短い `interval` ＋ 有界な `max_actions_per_cycle` と組み合わせることで、**持続的で制御可能な削除＋スキャン負荷**を作れる（ソークの土台）。

## 必要環境

- Go（リポジトリのモジュールでビルド）
- 投入先のデータディレクトリは**サーバー稼働中は使わないこと**（停止中に投入 → 起動、の順）

## 使い方

```bash
# 既定: 10万キー × 2バージョン、10日前、NoncurrentDays=1、NewerNoncurrentVersions=0
go run ./benchmark/soak/seed --data-dir /tmp/soak-data

# 100万キー、保持1（各キー 1 noncurrent を保護、もう1つを削除候補に）
go run ./benchmark/soak/seed --data-dir /tmp/soak-data \
  --keys 1000000 --versions 3 --age-days 10 --noncurrent-days 1 --newer-noncurrent 1
```

投入後、同じ data-dir に対して JOG を短周期で起動するか `jog lifecycle run` を実行する。

## オプション一覧

| フラグ | 既定 | 説明 |
|---|---|---|
| `--data-dir` | `./soak-data` | 投入先（無ければ作成）。`<dir>/metadata.db` に書く |
| `--bucket` | `soak` | バケット名 |
| `--keys` | `100000` | キー数 |
| `--versions` | `2` | キーあたりバージョン数（>=2、最新が current、残りが noncurrent） |
| `--age-days` | `10` | タイムスタンプを何日前にするか |
| `--noncurrent-days` | `1` | 投入する NCVE ルールの NoncurrentDays |
| `--newer-noncurrent` | `0` | NewerNoncurrentVersions（保持数、0=該当する全 noncurrent を削除対象に） |

## 出力の見方

```
done: 1000000 keys, 3000000 version rows, 1000000 expected deletions in 4.2s
```
- `expected deletions` = `keys × (versions - 1 - newer-noncurrent)`。サイクルを何回か回すと（`max_actions_per_cycle` で分割）この数だけ削除される。

## 想定ワークフロー

1. このツールでデータを投入。
2. JOG を `JOG_LIFECYCLE_INTERVAL`（短く）・`JOG_LIFECYCLE_MAX_ACTIONS`（有界）で起動。
3. 並行して Warp 等でリクエスト負荷をかける。
4. RSS / WAL サイズ / エラー率 / サイクルログを経時サンプリング。

詳細手順は [`benchmark/docs/LIFECYCLE_SOAK.md`](../../docs/LIFECYCLE_SOAK.md) を参照。

## 既知の制約

- **NCVE（noncurrent 物理削除）専用**。Expiration（current → delete marker）や EODM の負荷は current/DM の構成が必要で、このツールはカバーしない（手順書にバリエーションを記載）。
- データファイルを作らないため、`GetObject(versionId)` での取得確認はできない（削除ロジック・スキャン負荷の検証用）。
- 投入は**サーバー停止中**に行うこと。WAL は seeder の `Close()` でチェックポイントされる。
