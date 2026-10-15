# AmsHelper

VocaBox の再生ヘルパー。`AdaptiveMediaSource` + `MediaPlayer`（Windows 標準のメディアスタック）で
ニコニコ DOMAND の HLS 音声を再生する常駐プロセス。Go 側（`app/helper.go`）が子プロセスとして起動し、
stdin/stdout の JSON lines で制御する。クラッシュ分離と、Go に WinRT バインディングを持ち込まないことが目的。

## ビルド

```powershell
mise run helper   # リポジトリ root で。resources/AmsHelper.exe を出力
```

`x86_64-pc-windows-gnu` でビルドする（MSVC 不要）。MinGW-w64 の C ランタイムと `rust-lld` はツールチェーン同梱。出力は `resources/AmsHelper.exe`（単体・ランタイム不要・約2MB）。

## プロトコル

stdin（コマンド、1行1オブジェクト）:

| cmd | フィールド | 意味 |
|---|---|---|
| `play` | `url`, `cookie`, `ua`, `gen`, `play` | audio media playlist の署名URLを再生。既存の再生は置き換え。`gen` はこの再生の世代名（イベントにそのまま付く）、`play` が false なら停止状態で読み込む |
| `pause` / `resume` | | 一時停止 / 再開 |
| `seek` | `ms` | 位置移動（read-only 時は無視） |
| `volume` | `value` | 0〜1 |
| `stop` | | ソースを破棄して停止 |
| `position` | | 現在位置を `position` イベントで返す（プル方式） |
| `quit` | | 終了（stdin EOF でも終了） |

stdout（イベント、1行1オブジェクト）:

| ev | フィールド | 意味 |
|---|---|---|
| `opened` | `durationMs` | 再生開始。`NaturalDuration` |
| `ended` | | 最後まで再生した |
| `failed` | `code`, `ext`, `msg` | 再生失敗。`code` は `MediaPlayerError` 名か `AdaptiveMediaSourceCreationStatus` 名 |
| `position` | `ms` | `position` への応答 |

- `cookie` は `name=value; ...`。ヘルパーの HttpClient の Cookie ヘッダとして全リクエスト
  （manifest / segment / AES鍵）に載せる（`domand_bid` が無いと delivery/asset 系が 403）。
  システムの Cookie ストアは使わない（`CookieUsageBehavior.NoCookies`）ため、
  `user_session` などが OS 側に残ることはない。
- 位置のプルは Go 側が約4回/秒。イベントと応答は同じ stdout に混ざるため、`ev` で振り分ける。
- イベントには `gen` が付く（`play` で渡された世代名）。差し替えられた再生の遅れたイベントを
  アプリが見分けて捨てるためのもので、ヘルパー自身は `gen` を検証しない。
- 失敗時の音質フォールバック（次の帯で `play` し直し）は Go 側の仕事。ヘルパーは渡された URL だけを扱う。
