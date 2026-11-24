# Native module ams

ams is a native module of MyGo: Go interfaces implemented in Rust. Read
https://mygo.dev/docs/native-modules (docs/native-modules.md) before changing
it.

- `ams.go` declares the interface and the types it uses: it is the contract.
  `//mygo:native` marks an entry point, which gets a constructor, `NewPlayer`.
- `ams_native.go` and `rust/src/mygo.rs` are generated: never edit them; `go
  tool mygo dev` regenerates them when the interface changes.
- `rust/` is the implementation: `make_player` in `src/lib.rs` makes the
  player, which drives AdaptiveMediaSource and MediaPlayer on a thread of its
  own. The module is Windows-only, as the app is: there is no Swift or Zig
  implementation.

Check the module without opening the app:

```sh
go tool mygo native check     # generate, build and vet: every problem as file:line
go tool mygo native call ./modules/ams Player.SetVolume '{"v": 0.5}'
```
