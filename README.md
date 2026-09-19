# JevGate

JevGate は、2 つの Git revision の差分について、人間による承認を必須とせず AI コードレビュアーの承認を利用できるかを Jev で判定する CLI です。

コードレビュー、Pull Request の承認、危険性の判定そのものは行いません。確信度がしきい値に届かない場合は、人間によるレビューが必要という結果を返します。

## 現在の状態

CLI は実装済みです。GitHub Action、レビュアー連携例、リリースバイナリの配布は今後のタスクで追加します。

ソースからビルドするには Go と Git が必要です。

```console
go build -o jevgate ./cmd/jevgate
export JEV_API_KEY='your-api-key'
```

## 使い方

Git リポジトリ内で base と head の 2 revision を指定します。

```console
./jevgate main HEAD
./jevgate --base main --head HEAD
./jevgate origin/main HEAD --threshold 0.98
./jevgate main HEAD --format json
```

revision は最初に commit SHA へ解決され、その 2 点が直接比較されます。`main..HEAD` や `main...HEAD` のような range 式は受け付けません。未コミットの変更と未追跡ファイルは判定対象に含まれません。

主なオプション:

| オプション | 内容 |
| --- | --- |
| `--base <revision>` | base revision。`--head` と組み合わせて指定 |
| `--head <revision>` | head revision。`--base` と組み合わせて指定 |
| `--config <path>` | 設定ファイル。相対パスは実行ディレクトリ基準 |
| `--threshold <number>` | 許可に必要な確率を `0` から `1` で指定 |
| `--format text\|json` | 出力形式。デフォルトは `text` |
| `--help` | ヘルプを表示 |
| `--version` | バージョンを表示 |

## 設定

デフォルトではリポジトリルートの `.jevgate.yml` を読みます。ファイルがなければしきい値 `0.95` を使用します。

```yaml
threshold: 0.95

context: |
  This repository contains a Go API deployed to ECS.
  Files under docs/ contain documentation.
```

しきい値の優先順位は CLI の `--threshold`、設定ファイル、デフォルト値の順です。`context` は diff とともに Jev へ送信されます。API キーは設定ファイルへ書かず、`JEV_API_KEY` 環境変数で指定してください。

## 出力と終了コード

text 出力の例:

```text
AI approval allowed: 97.2%
Threshold:           95.0%

ALLOW
```

JSON 出力は次の 3 フィールドを持ちます。

```json
{"ai_approval_allowed":true,"confidence":0.972,"threshold":0.95}
```

`confidence` は「AI の承認を利用できる」という回答の肯定確率です。低い値は、その変更が危険だという意味ではありません。

| 終了コード | 意味 |
| --- | --- |
| `0` | AI の承認を許可 |
| `1` | 人間によるレビューが必要 |
| `2` | 入力、設定、Git、API、または出力エラー |

正常な判断は stdout、診断は stderr に出力されます。stdout への書き込み途中で失敗した場合は部分的な JSON が残る可能性があるため、連携側は JSON の存在だけでなく終了コードも必ず確認してください。終了コード `2` を `confidence: 0` の正常な否定判断として扱ってはいけません。

## 入力上の制限

空の diff、バイナリ、submodule の内部変更、上限を超える差分、完全性を確認できない diff は評価せず、終了コード `2` を返します。JevGate は差分を切り詰めたり、パスによってファイルを除外したりしません。
