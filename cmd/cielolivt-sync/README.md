# cielolivt-sync

Twitch の配信一覧をローカル JSON に蓄積し、YouTube アーカイブが公開されたら同じ配信日のままリンクだけ YouTube に差し替えて atwiki 形式で出力する小さな CLI です。

## 使い方

```sh
go run . \
  -seed-atwiki current.atwiki \
  -state cielolivt-streams.json \
  -out season2.atwiki
```

主な挙動:

- Twitch VOD から `createdAt` を JST の `yyyy/mm/dd` にして保存します。
- YouTube 側に同じ `#番号` の動画が見つかった場合、日付は既存の Twitch 日付を維持して YouTube リンクへ差し替えます。
- Twitch 日付がない YouTube 動画は `yyyy/mm/dd` で出力します。
- `-seed-atwiki` を渡すと、既存のatwiki表から日付とリンクを台帳へ取り込めます。
- 出力は既存ページに貼りやすい `#region(月)` と2列テーブルです。

## よく使うオプション

```sh
go run . -youtube-pages 10 -twitch-limit 50
go run . -youtube=false
go run . -twitch=false
```

`cielolivt-streams.json` が日付の台帳です。VOD が消える前に定期的に実行しておくと、後から YouTube アーカイブへ置き換えても配信日を失いません。
