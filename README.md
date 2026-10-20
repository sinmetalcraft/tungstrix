# tungstrix
Spanner最適化支援Agent

## Environment

API Keyを利用する場合

```
GOOGLE_API_KEY={YOUR_API_KEY}
```

Vertex AIを利用する場合

```
GOOGLE_CLOUD_PROJECT={YOUR_PROJECT_ID}
GOOGLE_CLOUD_LOCATION=us-central1
GOOGLE_GENAI_USE_VERTEXAI=true
```

## Development

```
go run cmd/agent/main.go web api webui
```

## querydump

負荷の高いQueryのSQL・QueryPlan・Query Statisticsをファイルに出力するlocal用のtoolです。

```
go run ./cmd/querydump -project=my-project -instance=my-instance -database=my-db -prefix=hoge
```

| flag | default | description |
|---|---|---|
| `-project` | (必須) | Spanner ProjectID |
| `-instance` | (必須) | Spanner InstanceID |
| `-database` | (必須) | Spanner DatabaseID |
| `-order` | `cpu` | `cpu` = 合計CPU使用量順 / `latency` = 平均レイテンシ順 |
| `-top` | `25` | 出力するQueryの件数 |
| `-prefix` | (なし) | 出力ファイル名の先頭に付ける文字列 |
| `-out` | `.` | 出力先ディレクトリ |
| `-params` | (なし) | QueryParameterの型を指定するJSONファイル |
| `-filter` | (なし) | Queryの絞り込み条件を`key=value`で指定 (複数指定可) |
| `-database-role` | (なし) | Fine-grained access controlのdatabase role (例: `spanner_sys_reader`) |
| `-plan-project` | `-project`と同じ | 実行計画を取得するSpanner ProjectID |
| `-plan-instance` | `-instance`と同じ | 実行計画を取得するSpanner InstanceID |
| `-plan-database` | (なし) | 実行計画を取得するSpanner DatabaseID。指定するとQuery Statisticsとは別のDBでEXPLAINする |

1Queryにつき3ファイルを出力します。

```
hoge-20260923-1430-CPUTOP01.sql  # Query Statisticsから取得したSQL
hoge-20260923-1430-CPUTOP01.txt  # 上記SQLのQueryPlan
hoge-20260923-1430-CPUTOP01.md   # execution_count, avg_rows_scanned などのQuery Statistics
```

* `YYYYMMDD-HHMM`は実行時のタイムスタンプで、1回の実行で出力される全ファイルで同じ値になります
* `-prefix`を指定しない場合は`20260923-1430-CPUTOP01.sql`のようにタイムスタンプから始まります
* 末尾の連番は`-order`で指定した指標のランキングです
* `-order=latency`の場合は`CPUTOP`が`LATENCYTOP`になります
* QueryPlanを取得できないQueryは`.txt`にエラーを記録して次のQueryに進みます

### filter

`-filter`で対象のQueryを絞り込めます。指定したTableを使っているQueryだけを見たい場合は`table`を使います。

```
go run ./cmd/querydump -project=my-project -instance=my-instance -database=my-db \
  -filter=table=WorkflowExecutions
```

| key | description |
|---|---|
| `table` | 指定したTableを使っているQueryに絞り込む |

* `-filter`は複数指定できて、ANDで結合されます
* 絞り込んだ結果に対して`-order`のランキングが振られるので、ファイル名は`CPUTOP01`から始まります
* 適用したfilterは各`.md`の`filter`行に記録されます
* `table`はQuery StatisticsのSQLのtextに識別子として現れるかで判定しています。そのためCommentや文字列リテラルにTable名が入っているだけのQueryもmatchします
* Table名が他の識別子の一部になっているだけのQueryはmatchしません。`table=WorkflowExecutions`は`WorkflowExecutionsByShardId...`というIndex名だけを含むQueryにはmatchしません

filterの種類を増やす場合は`tools/spanner/filter.go`に`QueryStatsFilter`の実装を足し、`ParseQueryStatsFilter`にkeyを追加してください。
SQL側は`WHERE`句の`/*FILTERS*/`が条件に置き換わります。

### QueryParameter

QueryParameterを含むQueryは、値を渡さなくてもSpannerが文脈から型を推論して実行計画を作ります。
ただし`UNNEST(@shardids)`のように文脈から型を決められない場合は`INT64`と仮定されるため、以下のように失敗します。

```
Values referenced in UNNEST must be arrays. UNNEST contains expression of type INT64
```

この場合、実行計画の取得に失敗したQueryの分だけ`{prefix}-YYYYMMDD-HHMM-params.json`が出力されるので、
`type`を埋めて`-params`に渡して再実行してください。

```json
{
  "6950124233627194671": {
    "shardids": { "type": "ARRAY<INT64>" },
    "statuses": { "type": "ARRAY<INT64>" }
  },
  "-6724744557130903353": {
    "startTimestamp": { "type": "TIMESTAMP", "value": "2026-09-01T00:00:00Z" },
    "endTimestamp": { "type": "TIMESTAMP", "value": "2026-09-02T00:00:00Z" }
  }
}
```

* keyは`text_fingerprint`です。順位(`CPUTOP09`など)は実行するたびに変わりますが、`text_fingerprint`はSQLの内容から決まるので使い回せます
* `value`は省略できます。省略した場合はその型のNULLをbindします。実行計画を取得するだけなら値は不要なので、通常は`type`だけ書けば十分です
* Change Streamの`READ_`関数のように実値が必要なものだけ`value`を書いてください
* 型推論で足りるQueryParameterは書かなくてよいです。`UNNEST`に渡すものなど、必要なものだけ書けば動きます
* 指定できる型は`BOOL` `INT64` `FLOAT64` `STRING` `BYTES` `DATE` `TIMESTAMP`と、それぞれの`ARRAY<>`です
* 型を間違えると`No matching signature for operator = for argument types: INT64, STRING`のように正しい型が分かるエラーが返ります

## readdump

Read API(ReadRow, Readなど)で読んだもののうち、合計CPU使用量が大きいものを`spanner_sys.read_stats_top_hour`から取得してmarkdownに出力するlocal用のtoolです。
SQLのQueryは含まれないので、querydumpと合わせて確認してください。

```
go run ./cmd/readdump -project=my-project -instance=my-instance -database=my-db -prefix=hoge
```

出力は`hoge-YYYYMMDD-HHMM-READTOP.md`の1ファイルで、ランキングの表とReadごとの`read_columns`などの詳細が入ります。

## lockdump

Lockの待ち時間が大きいRowを`spanner_sys.lock_stats_top_hour`から取得してmarkdownに出力するlocal用のtoolです。

```
go run ./cmd/lockdump -project=my-project -instance=my-instance -database=my-db -prefix=hoge
```

出力は`hoge-YYYYMMDD-HHMM-LOCKTOP.md`の1ファイルで、ランキングの表とRowごとの`sample_lock_requests`が入ります。

* `row_range_start_key`ごとに集約しているので、特定のRowにLockが集中しているかが分かります
* `sample_lock_requests`はsampleなので、Lockを要求した全てのColumnではありません

readdump、lockdumpのflagは`-project` `-instance` `-database` `-database-role` `-top` `-prefix` `-out` `-filter`で、意味はquerydumpと同じです。
`-filter=table=...`はreaddumpでは`read_columns`、lockdumpでは`row_range_start_key`と`sample_lock_requests`のColumn名で判定します。

## 名前の由来

Geminiに考えてもらいました。

`Tungstrix (タングストリクス)`

* 構成: Tungsten (タングステン/熱に強い最強の金属) + Strix (フクロウ科の属名)
* 意味: Spannerの高負荷（熱）にも耐えうる、最強の知能を持った監視者。
* 推しポイント: 語感が非常に強そうで、テクニカルで尖ったツールの印象を与えます。
