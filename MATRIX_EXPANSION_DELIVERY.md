# Matrix 组合展开交付说明

## 核对官方文档后的结论

已核对 GitHub 官方文档：

- [Workflow syntax: `jobs.<job_id>.strategy.matrix`](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#jobsjob_idstrategymatrix)
- [Running variations of jobs in a workflow](https://docs.github.com/en/actions/writing-workflows/choosing-what-your-workflow-does/running-variations-of-jobs-in-a-workflow)

确认如下：

1. 单个 matrix 最多生成 **256 个 jobs**。文档原文是 “A matrix will generate a maximum of 256 jobs per workflow run.”
2. 处理顺序是：原始行的笛卡尔积 → `exclude` 删除 → `include` 补充或新增。
3. 同一个 `include` 项可以匹配多个基础组合；该项中的附加键会应用到所有匹配组合。
4. 官方 fruit/animal 示例明确说明：
   - `{color: green}` 先加到所有原始组合；
   - 后面的 `{color: pink, animal: cat}` 匹配所有 `animal: cat` 的原始组合，并把先前由 include 添加的 `color: green` **覆盖**为 `pink`，不会因此创建新组合；
   - `{fruit: banana}` 匹配不到原始组合，因此成为新组合；
   - 后续 `{fruit: banana, animal: cat}` 不会合并到前一个 include 新建的 `{fruit: banana}`，因为匹配目标仍是原始笛卡尔积中未被 exclude 删除的组合。
5. GitHub 的 matrix 变量名实际上 **case insensitive**；官方 workflow syntax 文档明确给出 `OS` 与 `os` 等价的说明。当前 actionlint 解析器也已经把这些键规范化为小写。因此这里没有再引入一套大小写敏感比较，而是沿用现有解析和重复键检查行为；展开函数只使用 AST 中已经规范化的 map 键，不做额外大小写转换。这一点与任务描述中“大小写敏感”的字面说法及真实代码/文档均冲突，已按确认结果选择兼容现状。

## 展开规则

实现位于 `matrix_expand.go`，入口为内部函数 `expandMatrix(m *Matrix) (*matrixExpansion, bool)`。

### 1. 静态支持门槛

以下任一情况都会返回 `supported=false`，RuleMatrix 改走原来的 `checkExcludeLegacy`，RuleExpression 也保留原有宽松类型推导：

- 整个 `matrix: ${{ ... }}` 是表达式；
- 某一行是表达式，例如 `os: ${{ fromJSON(...) }}`；
- `include` / `exclude` 段或其中某个组合项是表达式；
- 任意 matrix 原始值、include/exclude 值或键中递归包含 `${{ ... }}`，因此也覆盖 `fromJSON(...)`；
- 同一个规范化键在 row/include/exclude 的静态值中同时出现对象值和标量值。

YAML alias 由 parser 预先 resolve；空 include 对象和空 exclude 列表按任务要求视为合法 no-op。

### 2. 基础笛卡尔积

- 行顺序尽量按源码位置排序，保证生成过程稳定；比较测试结果时仍会排序。
- 基础组合数为各行 `len(Values)` 的乘积，使用 `math/big.Int` 保存精确值。
- 没有原始 row 时，基础集合为空，而不是形式上的单个空 map。这样纯 `include` 矩阵中的每一项都会成为独立新组合。
- 某一行的值列表为空时，乘积为零。

### 3. exclude

每个 exclude 项对一个完整组合做子集匹配：

- exclude 中写出的每个键都必须存在于组合中；
- 值匹配直接使用现有 `isYAMLValueSubset`，因此对象 filter 可以只匹配对象的一部分字段，数组要求等长并逐元素子集匹配；
- 空 exclude 对象匹配所有组合。

所有 exclude 项依次从基础笛卡尔积中删除匹配组合。新的诊断检查每个 exclude 项是否匹配过**任意原始基础组合**；它不会把 include 新增值或 include 补充键当作可匹配目标。

当笛卡尔积过大而没有物化时，exclude 是否能匹配基础组合改为解析计算：exclude 中每个 row 键只要在该行存在至少一个子集匹配值，就能从各维度选出一个值组成匹配的笛卡尔组合。该判定与枚举结果等价。

### 4. include

每个 include 项按顺序处理，但匹配目标始终是 **exclude 后存活的原始基础组合**：

1. 只把“键名属于原始 matrix rows”的赋值当作匹配约束，并使用 `RawYAMLValue.Equals` 做精确匹配。
2. 所有原始 row 约束都满足的组合可能有零个、一个或多个。
3. 若有匹配组合：
   - 将该项中的非原始 row 键写入所有匹配组合；
   - 原始 row 键的值已经相等，不会被改写；
   - 非原始 row 键若已由更早的 include 添加，允许覆盖，这与官方文档 “added matrix values can be overwritten” 一致。
4. 若没有任何匹配的基础组合，将整个 include 项复制为一个新组合。
5. include 新建的组合不会成为后续 include 项的匹配目标。

因此，include 添加的非原始键及其后续覆盖不会改变“哪些原始基础组合匹配”；匹配只由原始 row 键决定。exclude 删除后的存活集合会影响匹配。

### 5. 大矩阵保护

常量：

```go
matrixMaxCombinations = 256
matrixExpansionEnumerationLimit = 4 * matrixMaxCombinations // 1024
```

- 基础乘积 `<= 1024`：物化笛卡尔积，并得到 exclude/include 后的精确最终数量和键覆盖情况。
- 基础乘积 `> 1024`：不生成组合 map；保留精确乘积字符串。exclude 诊断仍通过解析判定完成。数量诊断会明确说明该数字是 exclude/include 前的 Cartesian product，并提示枚举已在 1024 处停止。

## 键分类与表达式类型

展开结果用每个键出现在多少个最终组合中来区分：

- `keyCounts[k] == len(combinations)`：所有组合都有；
- `0 < keyCounts[k] < len(combinations)`：只在部分组合中存在，记录到 `partialKeys`；
- `keyCounts[k] == 0`：任何展开组合都没有。

第三类通常是拼写错误。由于 `checkMatrix` 构造的严格 object type 不包含未知键，现有 expression rule 仍会报告 `property ... is not defined in object type ...`。

对于部分存在的键，实现把已推导类型与 `NullType{}` 合并。当前 `NullType.Merge` / 具体类型 Merge 的冲突分支通常返回 `AnyType{}`，所以该键后续嵌套属性和参数类型检查会被放松为 any。这是准确建模“其他组合中为空”的代价；已确认接受。

部分键引用诊断默认关闭。库使用方可启用：

```go
ruleExpression.WarnPartialMatrixKeys = true
```

启用后，直接引用 `matrix.partial_key` 或 `matrix['partial_key']` 会产生 kind 为 `matrix-partial` 的诊断。CLI 当前没有新增专门开关；默认输出不会出现这类新诊断，以避免在官方常见 include 写法上大面积报错。

## 新增/变化诊断、关闭方式与误报风险

| 诊断 | Kind | 触发条件 | 关闭方式 | 典型误报/噪声风险 |
| --- | --- | --- | --- | --- |
| exclude 未匹配基础组合 | `matrix` | 静态可展开矩阵中，某个 exclude 项按完整子集匹配不到任何原始笛卡尔组合。位置在该条目起始键。 | CLI: `actionlint -ignore 'exclude" entry does not match any combination in the base matrix'`；或在 `actionlint.yaml` 的 `paths.*.ignore` 中配置同一正则。 | 最容易被感知为“误报”的是：exclude 引用只由 include 带入的键/值。旧 actionlint 不报错，但按 GitHub 顺序 exclude 先于 include，确实匹配不到；这是有意修正，也是兼容性说明中最需要灰度关注的一类。 |
| 组合数超限 | `matrix` | `<=1024` 时按最终展开数量报告 `matrix expansion produces N combinations...`；`>1024` 时按精确基础乘积报告 guard 诊断。 | `-ignore 'matrix expansion produces|matrix Cartesian product contains'`，或配置 `paths.*.ignore`。 | Guard 分支只根据 exclude/include 前的基础乘积提前报告。若矩阵极大且 exclude 规则覆盖面非常大，最终任务数可能被删到 256 以下；诊断文案已明确 “before exclude and include”。小矩阵（真实仓库的绝大多数情况）使用精确最终计数。 |
| 展开为零 | `matrix` | 基础乘积大于零，但所有基础组合被 exclude 删除，且没有 include 新组合，最终数量为 0。 | `-ignore 'matrix expansion contains 0 combinations'`。 | 若 workflow 依赖外部系统或 GitHub 未文档化的动态矩阵行为，静态分析可能无法确认；但所有含表达式/fromJSON 的矩阵都会放弃新检查，因此该风险主要限于纯 YAML 静态矩阵。 |
| 部分组合键 | `matrix-partial` | **默认关闭**；仅当库使用方设置 `RuleExpression.WarnPartialMatrixKeys=true` 且表达式直接引用 partial key 时报告。 | 默认无需关闭。若程序化启用，可用 `-ignore 'exists only in some expanded combinations'` 或 `paths.*.ignore` 过滤。 | 表达式可能已经用 `if: matrix.x != null`、`contains(matrix, 'x')` 或 GitHub 条件保证该键只在存在时使用；当前诊断不关联控制流/守卫条件，仍会在引用点提示。 |
| 未知 matrix 键（既有诊断） | `expression` | 键不在 row 或任何静态 include 赋值的并集中；严格 object type 报 undefined property。 | 沿用既有 `-ignore 'property .* is not defined in object type'` / config ignore。 | 动态 matrix 或 fromJSON 无法静态确定时走既有 expression matrix 推导/宽松 object，避免把动态属性误判为拼写错误。 |

配置文件示例：

```yaml
paths:
  .github/workflows/**/*.yml:
    ignore:
      - 'exclude" entry does not match any combination in the base matrix'
      - 'matrix expansion (produces|contains)'
      - 'matrix Cartesian product contains'
```

## 兼容性策略

- 含表达式、fromJSON、无法静态递归判断的值，或对象/标量同键混用时，所有新展开检查都不启用；RuleMatrix 完全保留旧 `checkExcludeLegacy` 行为。
- 旧的逐键 exclude 详细诊断在 fallback 矩阵中保持不变。
- 对静态可展开矩阵，exclude 诊断改为条目级；原有黄金文件 `matrix_exclude_mismatch`、`matrix_exclude_no_match`、`examples/matrix_checks` 已按新行为更新。
- `include: [{}]` 不再产生 “element ... should not be empty” 语法错误。
- `exclude: []` 不再产生 “exclude section should not be empty” 语法错误。
- `include: []` 仍沿用现有“空 section”风格诊断；本次只按明确要求放开空 include 对象和空 exclude 列表。
- 部分键默认不产生新诊断，只把类型放松为 any，避免官方常见 include 追加属性写法导致大量失败。

## 测试与黄金文件

新增独立表驱动测试 `matrix_expand_test.go`，覆盖：

- 基础笛卡尔积；
- 标量和对象 subset exclude；
- include 补充已有组合；
- include 新增组合；
- 无原始 row 键的 include 应用到所有组合；
- 补充额外键成功；
- 与原始 row 值冲突时成为新组合；
- 同一 include 匹配多个组合、后续 include 覆盖先前添加值；
- exclude 引用只存在于 include 的值；
- 272 组合超限；
- exclude 删光导致零组合；
- 表达式行/include 表达式放弃；
- 对象/标量混用放弃；
- 固定随机种子下乘积公式与枚举数量一致；
- 10^6 组合不被物化；
- `matrix-partial` 诊断默认关闭、可通过 `RuleExpression.WarnPartialMatrixKeys` 开启。

新增/更新的主要黄金文件：

- `testdata/err/matrix_exclude_only_in_include.yaml(.out)`
- `testdata/err/matrix_too_many_combinations.yaml(.out)`
- `testdata/err/matrix_product_enumeration_guard.yaml(.out)`
- `testdata/err/matrix_zero_combinations.yaml(.out)`
- `testdata/ok/matrix_empty_include_object_and_exclude_list.yaml`
- `testdata/ok/matrix_include_docs_expansion.yaml`
- `testdata/ok/matrix_partial_include_key_relaxed_to_any.yaml`
- 更新既有 exclude/empty-section 黄金输出。

验证命令：

```sh
go test . -count=1
```

根包全部测试通过。`go test ./...` 中 `scripts/check-checks` 与几个 generate 脚本存在失败：当前环境没有 `shellcheck`/`pyflakes`，且部分脚本测试需要访问 `raw.githubusercontent.com`；这些失败与 matrix 展开改动无关。
