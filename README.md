# VocaBox

ニコニコ動画の楽曲を聴く Windows デスクトップアプリ（個人開発・実験的）。Dwango 非公式です。

動画映像は表示せず、音声中心の UI で再生します。公開マイリストを URL / ID で取り込み、一覧から選んで再生します。再生は公式の埋め込みプレイヤーをアプリ内に表示し、非公式に解析された postMessage プロトコルで操作します。

## できること

- 公開マイリストの閲覧（ID または `nicovideo.jp` / `nico.ms` の URL）
- マイリストの保存・更新・削除（`%APPDATA%\VocaBox\mylists.json`）。保存内容から読み込むため一覧表示はオフラインでも可
- 再生 / 一時停止、シーク、音量、シャッフル、ループ、前へ / 次へ

## まだできないこと

- ログイン・非公開マイリスト、音源ダウンロード・オフライン再生（現状のオフラインは一覧表示のみ）

## セットアップ

Windows、Go 1.27.1 以降、Bun、WebView2 Runtime が必要です。

```sh
bun install
bun run dev     # 開発起動
bun run build   # build/ に出力
```

## 構成

- `app/` Go（MyGo Native UI、マイリスト取得・保存）
- `src/` TypeScript（埋め込みプレイヤーのラッパー、再生キュー）
- `player.html`, `tests/`, `scripts/`

## テスト

```sh
go test ./...
bun test
```

## ライセンス

[Mozilla Public License 2.0](LICENSE)。第三者製ソフトウェアは [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 参照。
