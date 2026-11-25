# module構文

`module` は、集合・関数・時相論理・モジュール・定理・階層証明を記述するための構文です。[TLA+2の公式文法](https://lamport.azurewebsites.net/tla/TLAPlus2Grammar.tla)と[Version 2 Guide](https://lamport.azurewebsites.net/tla/tla2-guide.pdf)の構成要素を、`.tlapr` からTLA+へ変換します。各構成要素とテストの対応は末尾に示します。

この対応範囲は、TLA+の各構成要素をDSLで表現できることを指します。`.tla` をそのまま入力するパーサーではありません。ブロック・宣言・式・証明を解析して出力し、生成後の名前解決と式のレベルはSANYで確認します。入力をそのまま出力するrawブロックはありません。

既存の `model` は `Int` / `Bool` の型検査、逐次代入、暗黙の `UNCHANGED`、`Init` / `Next` / `Spec` の生成を続けます。`module` ではTLA+の集合論に合わせて式を記述し、初期条件・遷移・仕様・型の不変条件を明示します。

## 実行方法

```sh
go run ./cmd/tlapr compile -o out examples/queue.tlapr
go run ./cmd/tlapr validate -jar .tools/tla2tools.jar examples/queue.tlapr
go run ./cmd/tlapr check -jar .tools/tla2tools.jar examples/queue.tlapr
go run ./cmd/tlapr validate -jar .tools/tla2tools.jar examples/proof.tlapr
```

| コマンド | 確認する内容 | 必要なもの |
| --- | --- | --- |
| `compile` | DSLの構文を解析し、TLA+・設定・宣言位置の対応表を生成 | Goのみ |
| `validate` | SANYで構文・名前・式のレベルを検査 | Javaとtla2tools.jar |
| `check` | TLCで指定した初期状態・遷移・不変条件・時相的性質を検査 | Javaとtla2tools.jar、`config` |

`validate` は証明義務を解くコマンドではありません。`OBVIOUS` や `OMITTED` の内容が正しいことは確認せず、証明の正しさには別途TLAPSなどが必要です。`compile` の成功だけでも、TLA+としての名前・レベルの妥当性は保証しません。

`module Queue` の出力名は `Queue.tla` / `Queue.cfg` / `Queue.map.json` です。識別子は保持します。`model` の出力名に付く `Tlapr_` は付きません。宣言位置の対応表はルートモジュールの宣言・定義を対象にし、式の各トークンや入れ子の証明の位置を逆変換するものではありません。

`validate` と `check` は入力ファイルと同じディレクトリの `.tla` をライブラリとして参照します。別の `.tlapr` モジュールを使う場合は、先にそのモジュールを同じディレクトリへコンパイルしてください。標準モジュールはjarから読みます。

## 宣言と定義

```text
module Example extends Integers, Sequences {
  const Limit, Predicate(_);
  var x, history;
  recursive Factorial(_);
  def Factorial(n) = if n == 0 then 1 else n * Factorial(n - 1);
  def Square[n in 0..3] = n * n;
  def Apply(F(_), value) = F(value);
  local def InternalValue = 1;
  assume PositiveLimit = Limit > 0;
}
```

文末の `;` は必須です。改行は空白と同じなので、長い式は自由に改行できます。`//` と `\*` は行コメント、`/* ... */` と `(* ... *)` は入れ子にできるブロックコメントです。1ファイルに1個のルートモジュールを置き、その中にモジュールを入れ子にできます。

`def F(x) = ...;` は演算子定義、`def f[x in S] = ...;` は関数定義です。`F(_)` や `F(_,_)` は引数に取る演算子の引数数を表します。`local` は定義と `instance` に付けられます。

`` def infix `++`(a,b) = ...; ``、`` def prefix `-`(a) = ...; ``、`` def postfix `^+`(a) = ...; `` でTLA+のユーザー定義演算子を定義できます。`` const infix `++`; `` のように宣言も可能です。単項マイナスの宣言・定義ではTLA+の特殊表記 `-.` を出力します。高階演算子へ単項マイナスを渡す場合は `` Apply(`-.`) ``、二項の減算なら `` Apply(`-`) `` と書きます。

DSLの予約語を名前にする場合もバッククォートを使います。例えば `` def `next` = true; `` と `` def Value = `next`; `` です。TLA+自身の予約語は変数や演算子の識別子にできません。ただし、公式文法で `Name` とされるモジュール名やレコードのフィールド名には使えます。例えば `` module `IF` {} ``、`` record {`IF`: 1} `` です。一部のフィールド名については後述のSANYの制限があります。

ちょうど `WF_` / `SF_` という名前も、引用の有無によらずモジュール名やレコードのフィールド名に使えます。例えば `module WF_ {}`、`record {WF_: 1}.WF_` です。変数名や演算子名には使えず、`WF_Value` のように続きがある名前も使えません。`WF_x(A)` / `SF_x(A)` は引き続き公平性の式です。

## 値と式

`true` / `false`、整数・小数・2進数 `\b101`・8進数 `\o17`・16進数 `\hFF`、文字列を記述できます。`module` の数値リテラルを32bitへ制限することはありません。`.25` はSANYとの互換性のため `0.25` に正規化します。TLCで評価できる数値には別途制限があります。

| 用途 | DSLの記述例 | 生成するTLA+ |
| --- | --- | --- |
| 等値・否定・論理 | `a == b`、`a != b`、`!p`、`p && q`、`p \|\| q` | `=`、`#`、`~`、`/\`、`\/` |
| 含意・同値 | `p implies q`、`p iff q` | `=>`、`<=>` |
| 集合と所属 | `{1, 2}`、`x in S`、`x notin S` | 集合列挙、`\in`、`\notin` |
| 集合演算 | `S union T`、`S intersect T`、`S difference T`、`S subsetof T` | `\cup`、`\cap`、`\`、`\subseteq` |
| 冪集合・和集合 | `powerset S`、`union S` | `SUBSET S`、`UNION S` |
| 内包・像集合 | `{x in S: P(x)}`、`{F(x): x in S}` | 対応するTLA+の集合内包 |
| 量化 | `forall x in S: P(x)`、`exists x: P(x)` | `\A`、`\E` |
| 選択 | `choose x in S: P(x)` | `CHOOSE` |
| タプル | `tuple(a,b)`、`<<a,b>>` | `<<a,b>>` |
| 直積 | `S cross T cross U` | `S \X T \X U` |
| 関数 | `function(x in S) => F(x)`、`functions(S,T)` | `[x \in S \|-> F(x)]`、`[S -> T]` |
| 適用・定義域 | `f[x]`、`f[x,y]`、`domain f` | 関数適用、`DOMAIN` |
| レコード | `record {a: 1, b: true}`、`r.a` | `[a \|-> 1, b \|-> TRUE]`、`r.a` |
| レコード集合 | `recordset {a: S, b: T}` | `[a: S, b: T]` |
| 更新 | `update(f) { [x] = @ + 1; .a.b = 3; }` | `[f EXCEPT ![x] = @ + 1, !.a.b = 3]` |
| 分岐 | `if P then A else B` | `IF P THEN A ELSE B` |
| 複数の条件 | `case { P -> A; Q -> B; else -> C; }` | `CASE P -> A [] Q -> B [] OTHER -> C` |
| ローカル定義 | `let { def F(x) = x + 1; } in F(2)` | `LET F(x) == x + 1 IN F(2)` |
| 演算子引数 | `Apply(lambda x: x + 1, 2)` | `LAMBDA`を演算子引数に渡す |
| 条件の列 | `all { P; Q; }`、`any { P; Q; }` | `/\`、`\/` のリスト |

量化や関数の束縛には `x, y in S, z in T`、`<<x,y>> in S` も使えます。`let` の中では関数定義・モジュールの `instance`・`recursive` 宣言も使えます。`@` は `EXCEPT` が更新する場所の元の値です。複数の更新節はTLA+の `EXCEPT` の意味に従い、`model` の逐次代入とは別の構文です。

TLA+の記号表記も対応する位置で使えます。例えば `\A`、`\in`、`[x \in S |-> x]`、`[r EXCEPT !.a = @ + 1]` です。DSLの論理演算 `&&` / `||` やコメント `//` と同じ表記のTLA+演算子を使うときは、`` a `&&` b `` や `` a `//` b `` のように引用します。引用した演算子はTLA+の意味を保ちます。

### 優先順位

`module` の式は次の順で結合します。数字が大きいほど先に結合し、同じ優先順位の二項演算子は左結合です。出力には括弧を付けるため、TLA+固有の優先順位の範囲と重なってもDSLで選んだ結合を保ちます。

| 優先順位 | 主な演算子 |
| --- | --- |
| 1 | `implies`、`=>` |
| 2 | `iff`、`leadsTo`、`<=>`、`~>`、`-+->` |
| 3 | `\|\|`、`\/` |
| 4 | `&&`、`/\` |
| 5 | 比較、所属、包含 |
| 6 | `@@`、`compose`、`\cdot` |
| 7 | `:>`、`<:` |
| 8 | 和集合・積集合・差集合 |
| 9 | `..`、`...`、`!!`、`##`、`$`、`$$`、`??` など |
| 10 | `+`、`-`、`++`、`--`、直積など |
| 13 | `*`、`/`、`\div`、`%`、列の連結 `\o`、引用した `&&` / `\|\|` など |
| 14 | `^`、`^^` |
| 15 | 単項演算子、prime、`^+` / `^*` / `^#` |
| 17 | 演算子・関数の適用、フィールド参照 |

単項演算子の範囲は括弧で明示できます。常に `x == 1` を要求する式は `always (x == 1)` です。`S cross T cross U` は3要素タプルの集合で、`(S cross T) cross U` とは異なります。

## 状態遷移と時相的性質

```text
def Init = x in {0, 1};
def Move(n) = x < 2 && next(x) == n;
def Next = exists n in 0..2: Move(n);
def Spec = Init && always stutter(Next, x) && weakFair(x, Next);
def Finished = eventually (x == 2);
```

`next(x)` は `x'` です。`model` と異なり、更新しない変数の `unchanged` も明示します。`stutter(A,v)` は `[A]_v`、`nonstutter(A,v)` は `<<A>>_v` です。`enabled A`、`unchanged v`、action合成 `A compose B` も記述できます。

`always` / `eventually`、`P leadsTo Q`、`P -+-> Q`、`weakFair(v,A)` / `strongFair(v,A)`、`temporalForall x: P` / `temporalExists x: P` に対応します。仕様は `Spec` と名付ける必要はありません。`config` にモデル検査で使う定義名を指定します。

公平性は仕様の仮定です。弱公平性は継続的に有効なaction、強公平性は繰り返し有効になるactionについて実行を要求します。TLCのテストでは、この仮定を消す・弱める変更で反例が見つかることも確認しています。

## モジュールと部分式

```text
module Outer extends Integers {
  module Inner {
    const Base, Transform(_);
    def Result = Transform(Base);
  }
  def Fixed = instance Inner with Base = 2, Transform = lambda x: x + 1;
  def Parameterized(value) = instance Inner with Base = value, Transform = lambda x: x;
  def Values = tuple(Fixed!Result, Parameterized(5)!Result);
}
```

`instance M;`、`local instance M with ...;`、`def I(x) = instance M with ...;` に対応します。`with` は定数・変数・演算子を置換し、演算子名や `lambda` も渡せます。

式には `label:: expr`、`label(x):: expr` でラベルを付けられます。`F!label` や `F!1`、`F!(args)`、TLA+の `<<` / `>>` / `:` / `@` selector、演算子から始まる部分式参照を使えます。番号による参照は、DSLの優先順位と括弧で構成した式を対象にします。

## 定理と証明

```text
module Reflexivity {
  theorem Identity = forall x: x == x;
  proof {
    step take_value: take x;
    step reflexive: x == x;
    obvious;
    qed by ref(reflexive);
  }
}
```

`theorem`、`lemma`、`proposition`、`corollary` に対応し、名前は省略できます。証明は `proof by ...;`、`proof obvious;`、`proof omitted;` または上のブロック形式で書きます。`proof` は `by` / `obvious` / `omitted` の前では省略できます。ブロックの `step` 名はTLA+の階層番号へ変換し、`ref(name)` で同じ階層または外側のstepを参照します。

| 証明の要素 | 記述例 |
| --- | --- |
| 仮定と結論 | `assume { new x; x in S; } prove P(x)` |
| 束縛のレベル | `new constant x`、`new variable x`、`new state P`、`new action A`、`new temporal F` |
| 演算子・範囲付きの束縛 | `new F(_)`、`new x in S` |
| 全称・存在の扱い | `take x in S`、`witness value`、`pick x in S: P(x)` |
| 事実・場合分け | `have P`、`case P`、`suffices P` |
| 関係式の連鎖 | `step first: A <= B;` に続く `step second: @ <= C;` |
| ローカル定義 | `define { def F(x) = x; def g[x in S] = x; }` |
| 使う事実と定義 | `use only Fact def F`、`hide def F`、`by only ref(stepName) def F` |
| モジュールへの参照 | `by module M def module M` |
| 終了 | `qed by ref(stepName)`、`qed obvious`、`qed omitted` |

各stepには `step name: 文;` と書きます。名前を省いた `step: 文;` はTLA+の匿名stepとして出力し、使える事実への追加という証明上の違いを保ちます。匿名stepは `ref` では参照できません。式・`case`・`pick`・`suffices` の後に証明を付けられ、証明の中にも証明ブロックを置けます。仮定に入れ子の `assume ... prove ...` を書くことや、`label::` を付けることも可能です。

証明stepの先頭に置く `@` は、直前の関係式の右辺を引き継ぎます。例えば `A <= B`、`@ <= C` は `A <= B`、`B <= C` の連鎖です。これは更新式の `EXCEPT @` とは別の構文です。

## TLC設定

```text
config {
  constant Limit = 3;
  constant Nodes = {n1, n2};
  specification Spec;
  invariant TypeOK, Safe;
  property Finished;
  checkDeadlock false;
}
```

`specification` の代わりに `init Init; next Next;` を指定できます。両方の方式を混在させることはできません。`constant N <- DefinedValue;` と、`constant N <- [Module] DefinedValue;` の置換も使えます。設定値は非負の整数・文字列・真偽値・モデル値・それらの集合です。負数や計算式には、例えば `def Negative = -1;` と `constant N <- Negative;` のように演算子を定義して置換してください。

`constant I!Value <- DefinedValue;` は、指定したinstanceの定義だけを置換します。`constant I!Child!Value = 2;` のように複数段のinstance名と値の直接指定も使えます。同じモジュールから作った別のinstanceには適用されず、`[Module]` によるモジュール単位の置換とは適用範囲が異なります。

`constraint`、`actionConstraint`、`symmetry`、`view` も指定できます。設定の意味は検証に使うTLA+ Tools v1.7.4付属のTLCの仕様に従います。`checkDeadlock` の既定値は `true`、CLIの `-allow-deadlock` は指定された設定を `false` に上書きします。`config` がないモジュールもコンパイル・SANY検査できますが、`check` は設定不足として失敗します。

## 対応範囲と検証

公式文法のproduction名と、主な検証用ソースの対応です。[modules](../compiler/testdata/modules) は実SANYを通し、[module_conformance](../compiler/testdata/module_conformance) は実TLCで状態・遷移または時相的性質を調べます。通常のGoテストでは生成文字列・構文エラー・名前の引用・証明の参照範囲も検査します。

| 公式文法の構成要素 | DSL / 主な検証用ソース |
| --- | --- |
| `Module`、`Unit`、変数・定数・再帰宣言 | `module` / `var` / `const` / `recursive`、module_system |
| `OpDecl`、`NonFixLHS`、演算子・関数定義 | `def`、`F(_)`、`prefix` / `infix` / `postfix`、module_system / operators |
| `Instance`、`Substitution`、`ModuleDefinition` | `instance ... with ...`、module_system / expressions |
| `Argument`、`Lambda`、`OpName`、`OpArgs` | 演算子引数・`lambda`・引数付き参照、operators / expressions / proofs |
| `GeneralIdentifier`、`InstOrSubexprPrefix` | `!` によるモジュール・部分式参照、expressions / module_system |
| `QuantifierBound`、`IdentifierOrTuple` | 有界・無界の束縛とタプル分解、expressions / proofs |
| `Expression` の値・演算・グループ化 | literals / operators / expressions |
| `Expression` の量化・選択・集合 | `forall` / `exists` / `choose` と集合式、expressions |
| `Expression` の関数・レコード・タプル・更新・直積 | expressions / collections |
| `Expression` のIF・CASE・LET・条件リスト・ラベル | expressions / junctions。LET内のRECURSIVEはVersion 2 Guideも参照 |
| `Expression` のaction・時相・公平性 | temporal / weak_fairness / strong_fairness |
| `Assumption`、`Theorem` | `assume` / `axiom` と4種類の定理宣言、module_system / proofs |
| `AssumeProve`、`InnerAssumeProve`、`New` | `assume { ... } prove ...`、proofs |
| `Proof`、`TerminalProof`、`NonTerminalProof` | `proof` と `by` / `obvious` / `omitted`、proofs |
| `Step`、`QEDStep` | `step` / `qed` と全stepの種類、proofs |
| `UseOrHide`、`UseBody` | `use` / `hide` / `by` とfacts・defs、proofs |
| 字句・コメント・引用名・単項マイナスの特殊表記 | literals / quoted_names / operators、lexer・式のGoテスト |

TLCが探索した値は、コンパイラの出力から生成しない手書きJSONと照合します。追加した集合モデルは2状態・1遷移、非決定的モデルは5状態・2初期状態・4遷移です。集合モデルでは、不変条件に通る関数更新の誤変換を状態値の照合で検出します。弱公平性の削除、強公平性から弱公平性への誤変換は、時相的性質の反例で検出します。

グラフ読取りはTLA+ Tools v1.7.4付属のTLCのDOT形式に対応し、値はTLCが正規化して表示した有限値の文字列として比較します。変数順・状態ID・文字列外の空白を除いて比較し、集合などの任意のTLA+式をテスト側で評価し直すことはありません。

### ツールの制限

構文を表せることと、TLCで評価できることは別です。[公式のツール制限](https://github.com/tlaplus/tlaplus/blob/master/general/docs/current-tools.md)のとおり、action合成などはTLCが実行できません。無限集合の探索、大きな数値、実数、時相量化、パラメーター付きinstanceにも制限があります。`validate` と `check` は、ツールのエラーやタイムアウトを検証成功にしません。

公開されたTLA+2文法はモジュール直下の `USE ONLY` を許していますが、TLA+ Tools v1.7.4付属のSANYはこの形式を拒否します。tlaprは指定された意味を保って生成し、`validate` は非0で失敗します。[再現用ソース](../compiler/testdata/tool_limits/module_use_only.tlapr)を置いています。モジュール直下の `USE`、証明stepの `USE ONLY`、`BY ONLY` は実SANYを通しています。

レコード集合のフィールドは公式文法では `Name` ですが、固定したSANYは `[IF : Nat]` を拒否します。一方、レコード値の `[IF |-> 1]`、`.IF` の参照と更新は受理します。この差も[再現用ソース](../compiler/testdata/tool_limits/recordset_reserved_field.tlapr)とCLIの失敗検知テストに残しています。

同じSANYは、レコード値の2番目以降のフィールドに予約語を使った `[value |-> 1, WF_ |-> 2]` も拒否します。先頭のフィールドに使った `[WF_ |-> 1]` は受理します。こちらも[再現用ソース](../compiler/testdata/tool_limits/record_later_reserved_field.tlapr)で、変換後の `validate` が失敗を返すことを検査しています。

これらは構文の変換と有限モデルの回帰検証です。コンパイラ全体の意味保存を数学的に証明したものではありません。PlusCalは別の言語であり、アルゴリズムコメントの翻訳はこの構文の対象に含みません。
