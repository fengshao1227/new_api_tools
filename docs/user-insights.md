# 用户画像与用量查询

“用户画像”是 New API Tool 内部管理员的只读查询入口。按邮箱、用户名或显示名找人，
查看账户资料、充值、模型使用和 Token 明细，为后续人工联系用户提供依据。
它使用 Tool 已有的管理认证，不向 BeatAPI 用户端开放，也不会发送邮件或修改账户余额。

## 查询一个用户

从导航的“用户画像”搜索并选中用户，或从“用户管理”的“查看画像”进入。
默认查询最近 30 天；支持预设时间、自定义时间及“全部现存记录”，再按模型精确筛选。
邮箱、用户名和时间筛选保留在页面上，无需先查数字 ID。

资料和余额是账户当前值，累计消费取账户的 `used_quota`。所选时间内的用量、模型表、
UTC 按日统计、日志分页和任务分页使用同一组已应用的时间与模型条件。
充值统计只受日期影响，不按模型拆分；累计充值不受时间或模型筛选影响。
首次/最后用量记录与有用量记录天数仅统计所选范围内的消费和失败尝试，不把后台退款算成用户活跃。
“有记录模型数”仍覆盖消费、失败及退款；最后登录是独立的账户字段。

复制邮箱与导出当前模型统计 CSV 只把已查询的数据交给管理员，不触发邮件投递。
CSV 包含查询对象与时间范围，并处理表格公式注入字符。

## 数字的含义

| 字段 | 口径 |
|---|---|
| 计费记录数 | `logs.type=2` 的记录数；任务回执、补扣可能各占一条，不代表去重后的请求数 |
| 失败尝试数 | `logs.type=5`；一次请求切换渠道重试可能写多条 |
| 退款流水数、退款流水额 | `logs.type=6`；独立展示，不与消费记录额直接相减 |
| 消费记录额 | 当前保留的消费日志金额；网关全额退款会把原行清零 |
| 账户累计消费 | `users.used_quota`，与有限保留期的日志汇总分开 |
| 任务总数与状态 | 主库 `tasks`，按提交时间和原始模型查询；不加到计费记录数上 |
| 成功充值 | 成功订单；保留原币汇总，未知币种明确提示，不直接当美元 |
| 到账本金 | 能从订单或自动充值冻结额度确认时才显示，未知时不从付款金额猜测 |

“全部现存记录”不保证涵盖已清理的历史日志。时间窗口为 Unix 秒的 `[start_time, end_time)`，
每日汇总按 UTC 分桶。缺表、旧版本未记录的字段与实际值为零是不同状态，页面会分别表示。

## Token 口径

- 输入优先使用日志里记录的 `other.input_tokens_total`。旧 Anthropic 语义的 `prompt_tokens`
  不含缓存时，补上已记录的缓存读取和写入；其他记录使用原始输入字段。
- 缓存写入优先 `cache_write_tokens`，否则取 `cache_creation_tokens` 与
  `cache_creation_tokens_5m + cache_creation_tokens_1h` 的较大值，避免重算同一批写入。
- 总 Token 为输入加输出；缓存属于输入的明细，不再额外加到总数。
- 原始输入字段单独保留，缓存字段附记录覆盖数。未采集的缓存数量显示未知。
- 明细元数据损坏或超过读取上限时，提示部分 Token 明细不可用，并使用原始输入字段；
  缺失的缓存无法补推。PostgreSQL 16 之前不支持安全 JSON 校验函数时，汇总保留原始 Token 列并提示降级。
- 当前日志没有独立持久化推理 Token 数量，显示“未记录”；不从推理强度或模型名称估算，
  也不把推理 Token 再加到通常已包含它的输出数量上。

## 管理接口

全部接口继承现有 `/api` 管理认证，读取响应不允许共享缓存。

| 接口 | 内容 |
|---|---|
| `GET /api/users?search=...` | 复用已有用户搜索 |
| `GET /api/users/:user_id/insights` | 档案、余额、充值、活跃指标、用量汇总、模型拆分、每日统计、任务状态 |
| `GET /api/users/:user_id/insights/logs` | 计费、失败尝试和退款记录分页 |
| `GET /api/users/:user_id/insights/tasks` | 任务状态与用量分页 |

统计与明细接受 `start_time`、`end_time`、`model`；日志另接收
`type=all|consume|error|refund`，任务支持状态筛选。明细 `page_size` 最大 100。
用户名、模型、时间和分页参数不能拼接进 SQL；查询使用参数绑定、超时与固定排序。

账户、订单、任务使用主库，日志使用 `GetLog()` 指向的日志库。聚合在数据库完成，
不同数据库之间不做 JOIN，不把某用户的全部原始日志加载到应用内存。
PostgreSQL 的详细 Token 统计先按用户、时间和模型过滤，再用物化中间结果解析 JSON、提取数值；
每条统计查询内，同一记录的元数据不会因多个汇总字段而反复解析。其他数据库和旧 PostgreSQL
的降级路径保持各自兼容查询，不要求新增窗口函数或修改日志索引。
返回字段采用白名单，不提供密码、完整 API Key、付款凭据、原始 Prompt、`private_data`
或整段日志 `other`。旧网关缺少可选字段时返回明确的可用性说明；数据库故障不能伪装成零消费。

## 代码地图

- `backend/internal/handler/user_insights.go`：请求参数、统一只读响应与错误映射；路由注册在 `user_management.go`。
- `backend/internal/service/user_insights*.go`：类型、档案/支付、数据库 Token 聚合与分页明细；主/日志库独立传入。
- `frontend/src/components/UserInsights.tsx`：用户搜索与已应用筛选，取消过期请求；`UserInsightsSummary.tsx`
  与 `UserInsightsDetails.tsx` 展示汇总和分页。
- `frontend/src/lib/user-insights.ts`：接口类型、UTC 时间、查询字符串、空值与中文提示、CSV 转义。
- 后端 `user_insights_test.go` 与 handler 的 `user_management_test.go` 覆盖数据与认证契约；
  前端 `scripts/user-insights.test.mjs` 验证时间、空值、CSV、指标及提示口径。

## 验证边界

后端用例应覆盖独立主库/日志库、时间半开区间、模型精确筛选、分页、重试多行、退款清零与
退款流水独立、缓存 Token 不重复累加、未采集字段、未知币种及管理员认证。
前端检查用户切换与迟到响应、查询草稿与已应用条件、零值/未知、CSV 转义和窄屏表格。
本地模拟数据只证明页面和查询行为，生产数据的完整性仍受网关实际记录及保留策略限制。

运行已有检查：

```sh
cd backend
go test ./... -count=1
go vet ./...
go build ./...
cd ../frontend
npm run build
node --experimental-strip-types --test scripts/user-insights.test.mjs
```

外部数据库测试默认跳过；只有明确指定独立测试库的 `TOOL_INSIGHTS_TEST_MAIN_DRIVER`、
`TOOL_INSIGHTS_TEST_MAIN_DSN`、`TOOL_INSIGHTS_TEST_LOG_DRIVER`、`TOOL_INSIGHTS_TEST_LOG_DSN`
及 `TOOL_INSIGHTS_TEST_ALLOW_FIXTURE_WRITES=1` 后，才运行 `TestUserInsightsExternalDatabases`。
测试会建表和清理自身创建的表，不能使用生产连接串。覆盖 PostgreSQL/MySQL 主库与
PostgreSQL/MySQL/ClickHouse 日志库的六种组合，并同时保留 SQLite 的默认测试。

## 发布到 BeatAPI 内部 Tool 实例

本功能位于 `fengshao1227/new_api_tools`，与用户网站 `erickkkyt/BeatAPI` 的 Actions 账单、
工作流和部署目标独立。不要把网站的额度限制推断成 Tool 也不能发布。
本地 `gh` 默认仓库可能指向上游 `james-6-23/new_api_tools`，所有发布和状态查询均明确传入
`--repo fengshao1227/new_api_tools`，推送只使用已核对过的 `origin`。

生产入口是 `https://beattool.fengshao1227.com/user-insights`。目前 nginx 指向回环端口 `1146`，
服务目录 `/home/ubuntu/beat-newapi-tools`，源码在其 `src/`，容器是 `beat-newapi-tools`。
部署前仍需核实 nginx、端口和容器对应关系，不能把同机或其他机器的 `1145` Tool 实例当成目标。

已获发布授权且检查通过后，推送目标提交，再检查该 SHA 的 `.github/workflows/build.yml`。
没有对应运行时，用同一工作流的 `workflow_dispatch` 触发；不要仅凭 push 成功就报告上线。

```sh
tool_release_sha="$(git rev-parse HEAD)"
git push origin "$tool_release_sha:main"
gh run list --repo fengshao1227/new_api_tools --workflow build.yml --commit "$tool_release_sha"
```

确认无对应运行、且远端 `main` 仍是目标提交时：

```sh
gh workflow run build.yml --repo fengshao1227/new_api_tools --ref main
```

要求两个架构构建、镜像合并、生产部署及健康检查均成功。生产部署实际在服务器使用
`limited` 构建器从源码构建 `beat-newapi-tools:local`，只重建该 Tool 服务；GHCR 多架构镜像
并非当前生产容器直接使用的产物。核对实际运行镜像与部署日志中的对应 manifest，不能把
`exporting config` 的摘要误认为 `docker inspect .Image`，也不能仅用源码目录的 HEAD 证明运行版本。
重建前给旧镜像保留独立回滚标签，不删除既有回滚资源。

上线验证至少包括：健康检查、三条画像接口匿名均返回 401、已授权只读查询真实用户、模型过滤
同时作用于汇总和明细、消费/Token 总量与模型/日期拆分对账，以及公开页面实际引用的新资源。
不得为验证而修改余额、发送邮件或打印认证值/完整客户响应。生产登录浏览器交互、匿名页面资源、
真实数据 API 和本地模拟 UI 是不同证据，应分别报告。

## 查询超时排查

先确认用户/类型/时间筛选命中现有日志索引，再比较筛选和聚合的执行耗时。即使只有约一万条记录，
嵌套投影被 PostgreSQL 展开后，对每个汇总字段重复做 JSON 校验和转换也可能耗尽请求预算；
小规模 fixture 和空数据接口通过不足以证明生产规模可用。
保持用户/时间/模型过滤在物化前，保留 `parsed` 与 `source_rows` 两层物化边界，
不要为缩短代码重新内联。统计仍采用原三条汇总查询，不变更大小写排序规则、NULL/空模型分组、
缓存未知/零值、退款或活跃日口径。不要以加大超时、缩小默认窗口、截断记录来掩盖性能问题。

用约 10,000 条、每条约 2 KB 元数据的独立测试库调用完整 `Report()`，
核对汇总/模型/日期一致性并记录耗时；计时排除建库和编译，避免把环境相关阈值写成易波动的测试。
必要时在只读事务与有限 `statement_timeout` 下，对同一生产筛选执行参数化 `EXPLAIN ANALYZE`，
只保留执行时间和聚合行数。发布后还需测完整接口，SQL 单条耗时不等于整个画像响应耗时。
