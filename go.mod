module github.com/hu-ja-ja/VocaBox_Desktop

go 1.27.1

require github.com/egoist/mygo v0.4.9-0.20261013221850-f0fdde1d0aac

// mygo は未リリースの api-review（PR #335: Cookies.List の確定形）を疑似バージョンで固定している。
// 次のリリースタグが出たらタグに置き換える。

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.46.0 // indirect
)
