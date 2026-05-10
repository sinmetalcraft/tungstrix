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

## 名前の由来

Geminiに考えてもらいました。

`Tungstrix (タングストリクス)`

* 構成: Tungsten (タングステン/熱に強い最強の金属) + Strix (フクロウ科の属名)
* 意味: Spannerの高負荷（熱）にも耐えうる、最強の知能を持った監視者。
* 推しポイント: 語感が非常に強そうで、テクニカルで尖ったツールの印象を与えます。
