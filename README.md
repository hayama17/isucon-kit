# isucon-kit

ISUCON の競技中に、手元の PC から全台へデプロイしてログを集計する CLI です。

```sh
go install github.com/hayama17/isucon-kit/cmd/isucon-kit@latest
cp examples/isucon-kit.yaml isucon-kit.yaml   # リポジトリのルートに置く

isucon-kit deploy          # build → 全台に配置 → ログを空にする → restart
isucon-kit status          # 手元のファイルと各台のファイルのハッシュを比べる
isucon-kit analyze alp     # 各台の access.log を手元の alp に流す
isucon-kit analyze slow    # 各台の slow log を手元の pt-query-digest に流す
```

1 周の流れは `deploy` → ベンチ → `analyze` です。deploy のたびに `analyze` のログを空にするので、集計は直前のベンチの分だけになります。

## 前提

- 各台に `~/.ssh/config` の alias でパスワードなしに入れる。`sudo` もパスワードなしで通る
- 手元に `alp` と `pt-query-digest` を入れておく
- nginx の `log_format` と slow query log は自分で設定する。alp の形式（json / ltsv）は設定に合わせる

## 設定

全体の例は [examples/isucon-kit.yaml](examples/isucon-kit.yaml) です。

- `build`: deploy の最初に手元で実行する argv
- `hosts[].files`: 手元のパス → 配置先。パーミッションは手元のファイルのものを使い、所有者は root になる
- `hosts[].restart`: 配置後に restart する unit。書いた順に restart する
- `analyze.<name>`: `log` を各台から読み、`cmd` の標準入力に流す

## やらないこと

- 切り戻し: `git revert` して `deploy` し直す
- 並列デプロイ: 3 台を順に処理しても数秒で終わる
- pprof: `go tool pprof -http=: http://isu1:6060/debug/pprof/profile` で足りる
- unit ファイルの配置後の `daemon-reload`: 必要になったら足す
