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
* QueryParameterを含むQueryなど、QueryPlanを取得できないものは`.txt`にエラーを記録して次のQueryに進みます

## 名前の由来

Geminiに考えてもらいました。

`Tungstrix (タングストリクス)`

* 構成: Tungsten (タングステン/熱に強い最強の金属) + Strix (フクロウ科の属名)
* 意味: Spannerの高負荷（熱）にも耐えうる、最強の知能を持った監視者。
* 推しポイント: 語感が非常に強そうで、テクニカルで尖ったツールの印象を与えます。
