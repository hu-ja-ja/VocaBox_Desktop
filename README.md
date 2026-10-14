# VocaBox

ニコニコ動画の楽曲を聴く Windows デスクトップアプリ（個人開発・実験的）。Dwango 非公式です。

動画映像は表示せず、音声中心の UI で再生します。マイリストを URL / ID で取り込み（ログイン中は自分の非公開マイリストも）、一覧から選んで再生します。
再生はアプリが取得した音声のみの HLS（ニコニコ DOMAND）を、Windows 標準のメディアスタック
（`AdaptiveMediaSource` + `MediaPlayer`）で再生する子プロセス（AmsHelper）に任せます。

## できること

- ホーム画面（保存したリストと、ログイン中は自分のマイリストをカードで表示。カードの右クリックでピン留め / 解除）
- サイドバー（ホームと同じ並びのライブラリ。ログイン中はアカウント名とアイコンを表示し、確認ダイアログ付きでログアウト）
- マイリストの閲覧（ID または `nicovideo.jp` / `nico.ms` の URL。ログイン中は自分の非公開マイリストも）
- マイリストの保存・更新・削除（`%APPDATA%\VocaBox\mylists.json`）。保存内容から読み込むため一覧表示はオフラインでも可
- 一覧はページ単位で読み込み、末尾の「続きを読み込む」で次のページを追加
- 再生 / 一時停止、シーク、音量、シャッフル、ループ、前へ / 次へ
- シャッフルは残りの曲を一度ずつ並べ替える順列方式（再生中の曲は先頭に固定）
- 再生キュー画面（再生バー右のボタン、または再生バーの余白クリックで開閉）。再生中の曲を先頭に表示。曲の並べ替え（ドラッグ）とキューからの削除、曲名ホバーでメニュー。プレイリスト画面は再生状態を表示せず、曲を再生するとキュー画面へ切り替わる
- 音質は取得可能な最高帯を自動選択（ニコニコにログインするとプレミアムの320kbpsまで。再生バーに再生中の帯を表示）
- ニコニコへのログイン（アプリ内のログイン画面でニコニコにログイン。パスワードはアプリを通らず、受け取ったセッションだけを `%APPDATA%\VocaBox\session.dat` に DPAPI 暗号化で保存）
- ウィンドウの位置・サイズを次回起動時に復元（`%APPDATA%\VocaBox\window-state.json`）。音量・シャッフル・ループも保存（`settings.json`）

## まだできないこと

- 音源ダウンロード・オフライン再生（現状のオフラインは一覧表示のみ）

## プラン

- [x] 順列シャッフル再生とキュー画面
- [ ] ランダム選曲で再生
- [x] ホーム画面
- [ ] 低画質動画付きの再生
- [ ] 結合プレイリストの作成/再生
- [x] マイリストインポートの改善
- [ ] バックグラウンド再生/トレイ実装
- [ ] 細かいUXの改善

## セットアップ

Windows、Go 1.27.1 以降、Bun、.NET SDK 10（+ MSVC ビルドツール。AmsHelper の NativeAOT ビルド用）が必要です。
ログインのセッション読み出しに MyGo の `Cookies`（APIレビュー #335 の確定形）を使うため、未リリースの
`mygo@api-review` を疑似バージョンで参照しています。次のリリースタグが出たらそれに置き換えます。

```sh
bun install
bun run dev     # AmsHelper をビルドして開発起動
bun run build   # AmsHelper をビルドして build/ に出力
bun run helper  # AmsHelper（resources/AmsHelper.exe）だけをビルド
```

## 構成

- `app/` Go（MyGo Native UI、マイリスト取得・保存、再生エンジンとキュー、ニコニコの取得）
- `helper/` C#（AmsHelper: 再生を担う常駐ヘルパー。プロトコルは `helper/README.md`）
- `resources/` 同梱物（AmsHelper.exe。`bun run helper` の出力）
- `scripts/` 開発起動・ビルドのラッパー

## テスト

```sh
go test ./...
VOCABOX_E2E=1 go test ./app -run TestPlaybackE2E   # 実回線/実再生の確認（数秒鳴ります）
```

## ライセンス

[Mozilla Public License 2.0](LICENSE)。第三者製ソフトウェアは [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 参照。
