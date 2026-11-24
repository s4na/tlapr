# tlapr

読み慣れた代入と条件式で状態遷移を書き、TLA+へ変換するGo製の仕様言語・CLIです。コンパイラはLLMを介さず、構文解析・型検査・コード生成を決定論的に行います。生成した仕様は[TLC](https://github.com/tlaplus/tlaplus)で検査できます。

現在は `Int` / `Bool` の状態、action、事前条件、不変条件を扱う最小実装です。Goの外部パッケージ依存はありません。TLA+への変換はGoだけで動き、モデル検査にはJavaとTLCが必要です。

## 使ってみる

Go 1.27以降を用意し、リポジトリで実行します。

```sh
go run ./cmd/tlapr compile -o out examples/inventory.tlapr
```

次の3ファイルを生成します。生成先は `-o` を省略すると `./out` です。

| ファイル | 内容 |
| --- | --- |
| `Tlapr_Inventory.tla` | TLA+の状態・初期値・遷移・不変条件 |
| `Tlapr_Inventory.cfg` | TLCの検査設定 |
| `Tlapr_Inventory.map.json` | 生成した識別子とDSLの名前・定義位置との対応 |

### TLCで検査する

Java 21と、公式リリースのTLC v1.7.4を使用します。以下はmacOS / Linuxでの取得例です。CLIがTLCを自動取得することはありません。

```sh
mkdir -p .tools
curl --fail --location \
  --output .tools/tla2tools.jar \
  https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar
printf '%s  %s\n' \
  936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88 \
  .tools/tla2tools.jar | shasum -a 256 -c -
export TLA2TOOLS_JAR="$PWD/.tools/tla2tools.jar"

go run ./cmd/tlapr check -allow-deadlock examples/inventory.tlapr
```

`check` は一時ディレクトリへ仕様を生成し、TLCを起動します。反例の状態変数をDSLの名前で表示し、action・不変条件には元ファイルの定義位置も示します。検査完了時に違反がなければ終了コードは0、違反・実行失敗・タイムアウト・キャンセルは非0です。

在庫の例では、在庫0が意図した終了状態です。TLCは実行できるactionがなくなった状態を標準でデッドロックとして扱うため、この例では `-allow-deadlock` を指定します。このオプションは**モデル全体のデッドロック検査を無効にする**ため、停止してはいけないモデルには付けないでください。不変条件の検査は続けます。

ガードを抜いた例も用意しています。

```sh
go run ./cmd/tlapr check examples/inventory_bug.tlapr
```

在庫が `2 → 1 → 0 → -1` と変化し、`nonnegative` の違反を報告します。このコマンドは失敗するのが正しい結果です。

### CLIオプション

```text
tlapr compile [-o DIR] [-allow-deadlock] FILE
tlapr check [-jar PATH] [-timeout 60s] [-allow-deadlock] FILE
```

フラグは入力ファイルより前に指定します。`-jar` を省略すると `TLA2TOOLS_JAR` を読み、`check` の制限時間は既定で60秒です。コンパイル結果のデッドロック検査も無効にする場合は、`compile` に `-allow-deadlock` を指定してください。

通常のバイナリとしても利用できます。

```sh
go build -o bin/tlapr ./cmd/tlapr
./bin/tlapr check -jar .tools/tla2tools.jar -allow-deadlock examples/inventory.tlapr
```

## 仕様を書く

```text
model Inventory {
  state {
    stock: Int = 10
    sold: Int = 0
    paused: Bool = false
  }

  action purchase {
    require !paused && stock > 0
    stock -= 1
    sold += 1
  }

  invariant nonnegative {
    stock >= 0
  }

  invariant conserved {
    stock + sold == 10
  }
}
```

- `state` は状態変数と初期値です。初期値には他の状態変数を参照しない定数式を使います。
- `require` はactionの実行条件です。偽の場合、そのactionは選択されません。actionの代入より前に書きます。
- `action` は1回の原子的な状態遷移です。複数のactionが有効なら、TLCはそれぞれの選択肢を調べます。定義順の実行ではありません。
- `invariant` は初期状態と到達する各状態で成り立つべき真偽値の式です。名前は省略できます。

文は改行または `;` で区切れます。コメントは `//` から行末までです。モデルには1個以上の状態変数とactionが必要で、actionには1個以上の代入が必要です。

| 種類 | 対応する構文 |
| --- | --- |
| 型とリテラル | `Int`、`Bool`、整数、`true`、`false` |
| 整数演算 | `+`、`-`、`*`、単項 `-` |
| 比較 | `==`、`!=`、`<`、`<=`、`>`、`>=` |
| 論理演算 | `&&`、`||`、`!` |
| グループ化 | `(式)` |
| 代入 | `=`、`+=`、`-=`、`*=` |

型の不一致、未定義の変数、構文エラーは入力ファイルの行・列を含む診断で報告します。TLA+の予約語と衝突しないように、生成側の識別子には用途ごとの接頭辞を付けます。

### 代入の順番と原子性

action内の代入は上から順に評価します。

```text
action advance {
  x += 1
  y = x
}
```

この場合、`y` には更新後の `x` が入ります。同じ変数へ複数回代入することもできます。一方、action全体は1回の遷移なので、途中へ別のactionが割り込むことはなく、不変条件の検査もactionの途中では行いません。

actionで代入しなかった変数は、TLA+の `UNCHANGED` により値を維持します。現実の実装で他の処理が割り込める境界を調べる場合は、その境界でactionを分けてモデル化してください。

## 検査できる範囲

この実装は、記述したモデルの不変条件とデッドロックをTLCで調べます。実装コードの生成、既存コードとの一致の証明、最終的に処理が完了することの検証は行いません。要求を正しくモデル化できているかは、仕様のレビューが必要です。

`Int` は有限個の値に自動制限されません。初期値とガードを設計し、到達状態が有限になるモデルを使ってください。無制限に増えるカウンタや非常に大きな状態空間は、検査が終わらない場合があります。タイムアウトは検証成功を意味しません。

TLCの整数実装に合わせ、整数リテラルは符号付き32ビットの範囲（−2,147,483,648〜2,147,483,647）に制限しています。TLCが評価する式の計算結果がこの範囲を超えると実行エラーになる場合があり、その場合は検証成功にしません。任意精度の整数演算を保証するものではありません。

現在はコレクション、文字列、actionの引数、関数、条件分岐・ループ、時相的な性質、公平性を扱いません。仕様の全状態を変えない遷移を許すTLA+の `Spec` を出力しますが、デッドロック検査を自動で無効にはしません。

## 開発と検証

```sh
go test ./...
go vet ./...
go build ./...
```

通常のGoテストはJavaやTLCなしで実行できます。演算子・結合順序、型と診断、actionごとの代入環境、ソース対応表、CLIの失敗処理などを検査します。

実際のTLCを使う検証は、`tlapr_integration` タグを付けて実行します。このタグを指定したのに `TLA2TOOLS_JAR` やJavaがない場合は失敗します。CIで検証が省略されたまま成功することを防ぎます。

```sh
TLA2TOOLS_JAR="$PWD/.tools/tla2tools.jar" \
  go test -tags=tlapr_integration -run '^(TestTLC|TestTLAPR)' -count=1 -v ./compiler
```

GitHub Actionsでは **「Go単体テスト」** と **「tlapr変換検証」** を別々のジョブで実行します。後者は固定バージョンのTLCをチェックサム検証して使用します。

### tlapr自身を使った変換の検査

[`compiler/testdata/conformance`](compiler/testdata/conformance) に、検証用の `.tlapr` モデルと手書きの期待値 `.json` を置いています。

1. `.tlapr` をこのコンパイラでTLA+へ変換する。
2. TLCで不変条件を検査し、到達する状態と遷移をすべて出力する。
3. 状態の値・初期状態・遷移の集合を、コンパイラから独立して書いたJSONの期待値と比較する。

不変条件だけでなく実際の値と遷移も比べるため、「更新と不変条件を同じように誤変換した」「actionが出力されず検査すべき状態へ到達しなかった」といった問題を検出できます。TLCが付ける状態IDや探索順には依存しません。

| モデル | 検査する性質 |
| --- | --- |
| `sequential.tlapr` | 記述順の代入、複数回の更新、原子性、未更新変数の維持 |
| `branching.tlapr` | 複数actionの分岐と合流、無効なguard |
| `cycle.tlapr` | 3状態の循環と、許される遷移の境界 |
| `arithmetic.tlapr` | 算術・比較演算と優先順位を手計算した結果との一致 |
| `booleans.tlapr` | Boolの全4組合せと論理演算の真理値表 |
| `namespaces.tlapr` | 名前の衝突回避、匿名不変条件、初期値、整数の上下限 |

JSONの `states` は状態変数の値、`initial` は初期状態の配列index、`transitions` は遷移元・遷移先のindex対です。TLCが探索する `Next` の遷移を期待値に記述し、`Spec` が許す暗黙のstutteringは追加しません。期待値は生成結果から自動更新しないでください。

[`compiler/testdata/violations`](compiler/testdata/violations) では、初期状態または遷移後に不変条件が破れるモデルを使い、TLCの終了コードと違反した不変条件名まで確認します。既存の在庫モデル・デッドロック検査も継続しています。

さらに、生成結果をテスト内で意図的に変更し、増分の間違い・actionの欠落・余計な遷移・不変条件の欠落を検出できることを確認します。余計な遷移のケースでは状態集合と不変条件の成否は変わらず、辺の比較だけが違いを捉えます。本体のコードを書き換えて実行するテストではありません。

これは有限なモデルによる回帰検証です。コンパイラ全体の意味保存を数学的に証明するものではありません。グラフの読取りは固定したTLC v1.7.4のInt/Bool向けDOT形式に限定し、未対応の形式を読み飛ばさずエラーにします。

## 参照

- [TLA+ / TLCの公式リポジトリ](https://github.com/tlaplus/tlaplus)
- [TLC v1.7.4](https://github.com/tlaplus/tlaplus/releases/tag/v1.7.4)
- [Lamportによる原子的なステップとTLA+変換の説明](https://lamport.azurewebsites.net/tla/tutorial/session6.html)
- [TLA+ツールの制限とコマンドラインオプション](https://lamport.azurewebsites.net/tla/current-tools.pdf)
