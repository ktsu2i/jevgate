# jevgate

**AI コードレビュアーの承認に任せられる変更かどうかを判定する CLI。**

[English](README.md) | 日本語

jevgate は、AI コードレビュアーを利用するチーム向けの CLI です。2 つの Git リビジョンを比較し、人間の承認を必須とせず、AI の承認に任せられるほど単純な変更かを [Jev](https://docs.typesafe.ai/api) で評価します。スコアが設定したしきい値に届かない場合は、人間によるレビューが必要と判定します。

jevgate が担うのはこの判定までです。コードレビューはレビュアーが行います。Pull Request の承認やマージ、リポジトリの承認ルールの変更は行いません。

- 変更されたファイル同士の関係を含め、差分全体を評価します。
- 承認を許可するしきい値と、リポジトリの背景情報を設定できます。
- ターミナルで結果を確認でき、JSON と終了コードで CI に組み込めます。

## クイックスタート

### 1. インストールする

jevgate はリリースアーカイブ、`go install`、または Homebrew でインストールできます。利用には **Git** と **Jev API キー**が必要です。評価時には Jev API へのネットワーク接続が必要です。

#### リリースアーカイブ

タグ付きリリースはまだ公開していません。[Releases ページ](https://github.com/ktsu2i/jevgate/releases)にリリースが存在し、後述の公開後検証を通過した後は、次の名前でビルド済みアーカイブを配布します。

| OS | アーキテクチャ | アーカイブ |
| --- | --- | --- |
| Linux | `amd64`, `arm64` | `jevgate_<version>_linux_<arch>.tar.gz` |
| macOS | `amd64`, `arm64` | `jevgate_<version>_darwin_<arch>.tar.gz` |
| Windows | `amd64`, `arm64` | `jevgate_<version>_windows_<arch>.zip` |

`<version>` はタグ先頭の `v` を含みません。各アーカイブには、`CGO_ENABLED=0` でビルドした `jevgate`（Windows は `jevgate.exe`）だけが入ります。同じリリースの `checksums.txt` をダウンロードし、展開前にアーカイブを検証してください。Linux または macOS では、次の `x.y.z`、OS、アーキテクチャを公開済みリリースと利用環境に置き換えます。

```sh
VERSION=x.y.z
OS=darwin
ARCH=arm64
ASSET="jevgate_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/ktsu2i/jevgate/releases/download/v${VERSION}"

curl -fLO "${BASE_URL}/${ASSET}"
curl -fLO "${BASE_URL}/checksums.txt"

if command -v sha256sum >/dev/null 2>&1; then
  grep "  ${ASSET}$" checksums.txt | sha256sum --check -
else
  grep "  ${ASSET}$" checksums.txt | shasum -a 256 --check -
fi

tar -xzf "${ASSET}"
./jevgate --version
./jevgate --help
```

#### Go

**Go 1.26 以降**が必要です。リポジトリ公開後は、次のコマンドでインストールできます。

```sh
go install github.com/ktsu2i/jevgate/cmd/jevgate@latest
```

現在、リポジトリは private です。公開までは、リポジトリへのアクセス権に加え、Git の認証設定と対応する `GOPRIVATE` の設定が必要です。`GOPRIVATE` をまだ設定していない場合の例：

```sh
GOPRIVATE=github.com/ktsu2i/jevgate go install github.com/ktsu2i/jevgate/cmd/jevgate@latest
```

すでに `GOPRIVATE` を利用している場合は、既存の設定を置き換えず、カンマ区切りのパターンに `github.com/ktsu2i/jevgate` を追加してください。

実行ファイルは `GOBIN` に、未設定の場合は `$(go env GOPATH)/bin` にインストールされます。そのディレクトリを `PATH` に追加してください。

#### Homebrew

[Homebrew](https://brew.sh/) を用意し、[専用tap](https://github.com/ktsu2i/homebrew-tap)から開発版をインストールします。

```sh
brew install --HEAD ktsu2i/tap/jevgate
```

まだリリースタグがないため、`--HEAD` で `main` からビルドします。ビルドに必要な Go と、実行時に必要な Git は Homebrew がインストールします。リポジトリが private の間は、アクセス権のあるアカウントでの Git 認証が必要です。

開発版を更新する場合：

```sh
brew update
brew upgrade --fetch-HEAD ktsu2i/tap/jevgate
```

インストール後、CLI が起動することを確認してください。

```sh
jevgate --help
```

### 2. API キーを設定する

```sh
export JEV_API_KEY='your-api-key'
```

API キーは環境変数 `JEV_API_KEY` で渡し、設定ファイルには書かないでください。サービスの認証については [Jev API ドキュメント](https://docs.typesafe.ai/api)を参照してください。

### 3. 変更を評価する

評価したい Git リポジトリ内で、ベースブランチと現在のコミットを比較します。

```sh
jevgate main HEAD
```

`main` は比較元のブランチ名やコミットに置き換えてください。2 つのリビジョン間には差分が必要です。以下は結果の例です。

```text
AI approval allowed: 97.2%
Threshold:           95.0%

ALLOW
```

`ALLOW` は、AI の承認に任せるためのしきい値をスコアが満たしたことを示します。しきい値未満の場合は `HUMAN REVIEW REQUIRED` と表示します。デフォルトのしきい値は `0.95` です。

## 使い方

ブランチ名、タグ、コミット SHA を 2 つ指定します。位置引数、または `--base` と `--head` のどちらかを使ってください。

```sh
jevgate main HEAD
jevgate --base main --head HEAD
jevgate origin/main HEAD --threshold 0.98
jevgate main HEAD --format json
```

両リビジョンをコミット SHA に解決して、その 2 点を直接比較します。merge base（共通祖先）は自動選択しません。リビジョンは個別に指定してください。`main..HEAD` や `main...HEAD` のような範囲指定には対応していません。未コミットの変更と未追跡ファイルは対象外です。

| オプション | 説明 | デフォルト |
| --- | --- | --- |
| `--base <revision>` | 比較元。`--head` と併用し、位置引数とは混在させない | フラグで指定する場合は必須 |
| `--head <revision>` | 比較先。`--base` と併用し、位置引数とは混在させない | フラグで指定する場合は必須 |
| `--config <path>` | 設定ファイル。相対パスは実行ディレクトリ基準 | リポジトリルートの `.jevgate.yml` |
| `--threshold <number>` | AI の承認を許可する最低スコア。`0` 以上 `1` 以下 | `0.95` |
| `--format text\|json` | 出力形式 | `text` |
| `-h`, `--help` | ヘルプを表示 | — |
| `--version` | バージョンを表示 | — |

## 設定

必要に応じて、評価対象のリポジトリルートに `.jevgate.yml` を作成してください。

```yaml
threshold: 0.95

context: |
  This repository contains a Go API deployed to ECS.
  Files under docs/ contain documentation.
```

`context` には、Jev が変更を理解するためのリポジトリの背景情報を記述します。しきい値の優先順位は、CLI の指定、設定ファイル、デフォルト値の順です。設定ファイルがなければ、しきい値 `0.95`、追加の背景情報なしで動作します。

別の設定ファイルを使う場合：

```sh
jevgate main HEAD --config ./review-policy.yml
```

## 結果の読み方

`confidence >= threshold` の場合に AI の承認を許可します。`confidence` は「AI の承認に任せられるか」という問いに対する Jev の肯定確率です。低い値は人間によるレビューが必要という意味であり、その変更が危険だという意味ではありません。

評価では、ドキュメントの修正やコメントのみの編集など、影響を明確に理解できる単純な変更を対象として考えます。評価基準では、ビジネスロジック、認証、インフラ、依存関係、その他の動作に関わる変更に人間の承認を求めます。ファイルパスや変更の種類だけで自動的に許可することはありません。

`--format json` を指定すると、次の 3 フィールドを出力します。

```json
{
  "ai_approval_allowed": true,
  "confidence": 0.972,
  "threshold": 0.95
}
```

| 終了コード | 意味 |
| --- | --- |
| `0` | AI の承認を許可 |
| `1` | 人間によるレビューが必要 |
| `2` | 入力、設定、Git、API、または出力のエラーで評価に失敗 |

判定結果は stdout、診断メッセージは stderr に出力します。終了コード `2` は、有効な判定結果が得られなかったことを示します。出力エラーで不完全な結果が残る可能性があるため、JSON を利用する前に必ず終了コードを確認してください。

## CI での利用

両リビジョンを含むチェックアウト内で CLI を実行し、CI のシークレット管理機能から `JEV_API_KEY` を渡してください。比較元と比較先には正確なコミット SHA を指定します。shallow clone では、リビジョンを解決するために履歴の追加取得が必要な場合があります。

AI の承認を許可した場合だけチェックを成功させるなら、CLI の終了コードをそのまま利用できます。結果によってレビュアーを振り分ける場合は、終了コード `1` を「人間レビューが必要」という判定として、終了コード `2` を実行失敗として扱ってください。後続の処理に渡すのは、終了コードが `0` または `1` で、正常にパースできた JSON のみとし、エラーをスコアゼロの判定に置き換えないでください。

専用の GitHub Action や、そのまま利用できる GitHub Actions ワークフローはまだ提供していません。CLI の結果とレビュー・承認プロセスの連携は、利用側で設定する必要があります。

## Jev に送信するデータと入力制限

jevgate は、差分、変更されたファイルのパスとメタデータ、設定した `context` を評価のために Jev API へ送信します。

差分の切り詰めやパスによる除外は行わず、変更全体を評価します。以下の場合は終了コード `2` で評価を中止します。

- 空の差分、バイナリの変更、submodule に関わる変更。
- 不正な UTF-8 など、差分やファイルパスを忠実に表現できない場合。
- 入力上限を超える変更。収集する Git 出力は 128 KiB、エンコード後の API リクエストは背景情報と評価指示を含めて 128 KiB が上限です。

## トラブルシューティング

| 問題 | 確認すること |
| --- | --- |
| `jevgate: command not found` | Go の場合は `GOBIN` または `$(go env GOPATH)/bin`、Homebrew の場合は Homebrew の `bin` ディレクトリが `PATH` に含まれているか確認してください。 |
| `JEV_API_KEY is not set or is empty` | jevgate を実行するシェルや CI ステップで API キーを設定してください。 |
| `unusable revision` | ブランチ名やコミットを確認し、不足する履歴を取得してください。 |
| `there is no change to evaluate` | 内容の異なるコミットを指定してください。作業ツリーの編集は対象外です。 |
| 終了コード `2` | stderr で原因を確認してください。承認可否の判定は得られていません。 |

## リリース手順

`main` が更新されるたびに、tagpr が release pull request を作成・更新します。この pull request は `internal/cli/version.go` と `CHANGELOG.md` を更新します。内容を確認し、その version を公開するときだけマージしてください。minor または major version に上げる場合は、release pull request に `tagpr:minor` または `tagpr:major` ラベルを付けます。初回を `v0.1.0` にする場合は `tagpr:minor` を付けます。

release pull request をマージすると、`.github/workflows/tagpr.yml` が version tag と draft GitHub Release を作成します。後続の `assets` job が GoReleaser を実行し、その draft を再利用して全対応 archive と `checksums.txt` を生成し、release を公開します。version tag を手動で作成・pushしないでください。`GITHUB_TOKEN` で作成した tag は別の tag 起点 workflow を発火しないため、同じ workflow 内でリリース処理を続けます。

tagpr を利用するには、リポジトリ設定の **Allow GitHub Actions to create and approve pull requests** を有効にする必要があります。release pull request は、マージ前に通常の CI を通してください。release workflow は Jev API キーやレビュアーの認証情報を必要としません。

公開せずに GoReleaser の出力をローカルで確認する場合：

```sh
goreleaser check
goreleaser release --snapshot --clean
```

snapshot の生成先は Git から除外した `dist/` です。公開後は Releases ページに期待する asset があることを確認し、ダウンロードした archive を `checksums.txt` で検証します。`jevgate --version` と `jevgate --help` も確認してから、その version を公式 Workflow 例で利用してください。

不具合の報告や機能の要望は [Issue](https://github.com/ktsu2i/jevgate/issues) にお寄せください。
