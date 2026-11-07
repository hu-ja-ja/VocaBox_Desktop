# VocaBox

[![Latest release](https://img.shields.io/github/v/release/hu-ja-ja/VocaBox_Desktop?style=flat-square)](https://github.com/hu-ja-ja/VocaBox_Desktop/releases/latest)
[![License](https://img.shields.io/github/license/hu-ja-ja/VocaBox_Desktop?style=flat-square)](LICENSE)
[![Listed on Awesome MyGo](https://awesomemygo.dev/badge.svg)](https://awesomemygo.dev/apps/vocabox)

ニコニコ動画のマイリストを音声で楽しむ、Windows向けプレーヤーです。動画映像は表示せず、曲を選んで再生できます。

[ダウンロード](https://github.com/hu-ja-ja/VocaBox_Desktop/releases) ・ [ソースコード](https://github.com/hu-ja-ja/VocaBox_Desktop)

## 主な機能

- 公開マイリストをURLまたはIDから読み込み。ニコニコ動画のアカウントでログインすると、自分のマイリストや非公開マイリストも利用できます。
- 再生、一時停止、曲送り、シャッフル、ループ、再生キュー。
- 歌詞の表示。再生位置に合わせて行が切り替わり、オフセットも調整できます。
- 複数のマイリストをまとめて再生。
- マイリストごとの音源保存とオフライン再生。保存する音質を選べます。
- タスクトレイでのバックグラウンド再生と、キーボードのメディアキーに対応。
- Discord Rich Presence。有効にすると、再生中の曲（曲名・投稿者・サムネイル）をDiscordのプロフィールに表示します。
- 日本語・英語・中国語（簡体字・繁体字）に対応。

## 使い方

1. [ダウンロードページ](https://github.com/hu-ja-ja/VocaBox_Desktop/releases)からWindows 64ビット用インストーラーをダウンロードして実行します。
2. 「マイリストを読み込む」からURLまたはIDを入力します。公開マイリストはログインせずに読み込めます。
3. 曲を選ぶと再生が始まります。自分の非公開マイリストを使う場合は、アプリ内からニコニコにログインしてください。
4. 音源をオフライン保存するには、マイリストの詳細画面またはメニューから「オフライン保存」を選びます。
5. 再生中は、プレイヤーの「歌詞を表示」から歌詞を表示できます。

## 対応環境とプライバシー

- Windows 64ビットが必要です。マイリストや音源の取得にはインターネット接続が必要です。保存済みの音源はオフラインで再生できます。
- ログインにはニコニコのログイン画面を使います。パスワードはVocaBoxに保存せず、ログイン状態はWindowsのユーザーアカウントで保護してこのPCに保存します。
- マイリストと音源の取得ではニコニコ動画に、歌詞の取得では外部の歌詞データベースに、更新確認ではGitHubに接続します。歌詞の取得では曲名と投稿者名を検索に使います。
- Discord Rich Presenceを有効にしたときだけ、再生中の曲名・投稿者・サムネイルのURLを、このPCで動作中のDiscordクライアントに渡します。
- 音源の提供状況やアカウントの状態によって、再生できない曲があります。
- オフライン保存した音源は、このPCで自分が聴くためのものです。配布、公開、譲渡はしないでください。

VocaBoxはニコニコ動画の非公式アプリであり、株式会社ドワンゴとは関係ありません。

## ライセンスと問い合わせ

本アプリは [Mozilla Public License 2.0](LICENSE) のもとで公開しています。[サードパーティライセンス一覧](THIRD_PARTY_NOTICES.md)もご覧ください。

[不具合の報告・要望](https://github.com/hu-ja-ja/VocaBox_Desktop/issues)

## 開発

Windows環境に[mise](https://mise.jdx.dev/)をインストールし、リポジトリのフォルダーで実行します。

```sh
mise install
mise dev
```

表示言語を指定して起動する場合(引数を省略するとOSの言語に追従):

```sh
mise dev ja       # 日本語
mise dev en       # 英語
mise dev zh-Hans  # 中国語（簡体字）
mise dev zh-Hant  # 中国語（繁体字）
```

ビルドとテスト:

```sh
mise build
go test ./...
```
