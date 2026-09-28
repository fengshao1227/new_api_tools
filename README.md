<p align="center">
  <img src="./docs/images/banner.svg" alt="newapi-tool banner" width="100%" />
</p>

<p align="center">
  <img alt="Docs" src="https://img.shields.io/badge/docs-%E4%B8%AD%E6%96%87-E05243?style=for-the-badge" />
  <img alt="Go" src="https://img.shields.io/badge/Go-1.25.6-00ADD8?style=for-the-badge&logo=go&logoColor=white" />
  <img alt="Gin" src="https://img.shields.io/badge/Gin-1.11-008ECF?style=for-the-badge" />
  <img alt="React" src="https://img.shields.io/badge/React-19-61DAFB?style=for-the-badge&logo=react&logoColor=111827" />
  <img alt="Vite" src="https://img.shields.io/badge/Vite-8-646CFF?style=for-the-badge&logo=vite&logoColor=white" />
  <img alt="Database" src="https://img.shields.io/badge/DB-PostgreSQL%20%7C%20MySQL-4169E1?style=for-the-badge&logo=postgresql&logoColor=white" />
  <img alt="Redis" src="https://img.shields.io/badge/Cache-Redis%20%7C%20Memory-DC382D?style=for-the-badge&logo=redis&logoColor=white" />
  <img alt="Docker" src="https://img.shields.io/badge/Docker-Ready-2496ED?style=for-the-badge&logo=docker&logoColor=white" />
  <img alt="Port" src="https://img.shields.io/badge/Port-1145-0EA5E9?style=for-the-badge" />
</p>

# NewAPI-Tool | NewAPI 增强管理中间件

**NewAPI-Tool** 是面向 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 的增强管理中间件。它以旁路方式连接 NewAPI 数据库和缓存服务，把仪表盘、充值审计、毛利与来源分析、用户分析、模型监控和运维配置集中到一个独立管理后台中。

它的核心原则是**零侵入运行**：不修改 NewAPI 源码，不改变 NewAPI 原有表结构，不接管 NewAPI 主服务流量；只在管理员需要审计、分析、批量处理或扩展运营能力时提供额外工作台。

## 项目信息

| 项目 | 说明 |
|---|---|
| 项目定位 | NewAPI 的增强管理层，用于可视化、审计、风控和后台运维 |
| 上游项目 | [QuantumNous/new-api](https://github.com/QuantumNous/new-api) |
| 运行方式 | 独立容器 / 独立进程，连接 NewAPI 现有数据库 |
| 默认端口 | `1145` |
| 后端栈 | Go `1.25.6`、Gin `1.11`、sqlx、Redis |
| 前端栈 | React `19`、Vite `8`、TypeScript、Tailwind CSS、ECharts |
| 数据库 | 生产优先 PostgreSQL / MySQL，查询字段以导出的真实 schema 为准 |
| 部署入口 | `install.sh` 一键部署，或 `docker-compose.yml` 手动部署 |
| 镜像 | `ghcr.io/james-6-23/new_api_tools:latest` |

## 能力速览

| 模块 | 能力 |
|---|---|
| 经营仪表盘 | 最上面是增长（本月/累计注册、付费、收入 + 按日/按月趋势表）；下面按今天 / 7 天 / 30 天看计费额、供应商成本、实际消费收入与毛利（与毛利页同口径）、现金收入，注册转化（按来源 / 国家），赠额发放与负债、网关风控待审与扣住金额，异步任务成败与失败原因、退款，上游账户与未关闭告警，定价缺口（无成本表达式的模型、未定价调用），以及排除免费模型后的模型排行。风控页、上游监控、告警表不存在时对应卡片显示「无数据」。 |
| 来源分析 | 按 BeatAPI 首触来源的一级渠道 / 二级明细、注册国家（`signup_country`）和赠额档位（`grant_region`，附实发赠额）拆分注册用户，并对照成功充值标记已付费、未付费和付费率；排除面板白名单（内部账号、管理员）。 |
| 毛利分析 | 按消费日志核算已消费收入、供应商成本、赠额/免费成本和内部成本，并按日、模型、渠道、用户拆分。付费客户 = 有成功充值单或 `topup_quota > 0`（线下结算、管理员加余额的企业客户也算）；内部成本只算管理员和面板白名单——被加过余额的测试号要进白名单。成本基准读 new-api 价格簿（牌价）与成本基准，零售价按模型主分组的 `GroupRatio` 折算（主分组 = `AutoGroups` 里第一个有该模型的组），其他倍率不同的分组逐个列出；网关读失败时显示上次结果并标明。 |
| 充值审计 | 查询全量充值记录，按状态、渠道、时间和用户维度筛选，提供财务汇总、支付分布、漏斗和异常分析。 |
| IP 分析 | 按国家/地区的流量分布（世界地图 + 排名，基于 Top IP 样本并标明覆盖率）、单个 IP 反查，以及用户 / 令牌的只读风险画像。风控本身在网关 new-api 中执行。 |
| 模型监控 | 需登录的模型状态看板，支持时间窗口、刷新间隔、排序和分组。 |
| 渠道监控 | 渠道状态、测速、窗口请求量与错误率、单点模型；余额逐渠道显示上游自报值（币种以上游为准），不做合计。 |
| 用户与令牌运维 | 用户列表带是否付费、剩余赠额、注册国家 / 赠额档位、网关风控状态和全部登录方式（含控制台配置的 OAuth 提供方），可按分组与登录方式筛选；经网关管理接口封禁/解封（封禁理由对用户可见）。令牌按网关的有效状态统计（手动禁用、已过期、额度耗尽分开），批量禁用/启用写入数据目录下的 `token_audit.jsonl`，令牌页列出最近操作。 |

## 架构边界

- **零侵入**：NewAPI-Tool 只作为增强管理层运行，不要求改动 `new-api/` 源码。
- **不改 NewAPI schema**：所有涉及 NewAPI 数据表的查询和写入都遵循现有字段、类型和索引语义。
- **审计优先**：核心能力以查询、可视化、复核和运维辅助为主，批量写操作只覆盖明确的管理场景。
- **面向生产数据规模**：`logs` 等大表查询采用索引、缓存、超时和估算策略，避免无意义全表扫描。
- **双部署形态**：生产环境可用 PostgreSQL + Redis，单机或测试场景也可使用 SQLite / Memory 相关轻量缓存能力。

## 快速部署

### 方式一：一键脚本（推荐）

如果 NewAPI 已部署在 Linux 服务器上，可以使用一键脚本自动检测环境并部署：

```bash
bash <(curl -sSL https://raw.githubusercontent.com/james-6-23/new_api_tools/main/install.sh)
```

脚本会自动定位 NewAPI 安装目录、读取数据库配置、生成必要密钥、设置管理员密码、配置 Docker 网络并启动服务。部署完成后访问：

```text
http://your-server-ip:1145
```

### 方式二：Docker Compose 手动部署

适用于熟悉 Docker 的用户或非标准环境：

```bash
git clone https://github.com/james-6-23/new_api_tools.git
cd new_api_tools
cp .env.example .env
vim .env
docker-compose up -d
```

### 日志分库（LOG_SQL_DSN）自动兼容

部分 NewAPI fork 支持 `LOG_SQL_DSN`，把 `logs` 表整张分离到**独立日志数据库**（MySQL、PostgreSQL 或 ClickHouse）。这种部署下主库的 `logs` 表会被冻结、不再更新——本工具若只连主库，则**仪表盘的计费/成本/毛利与模型排行、使用日志、模型监控、IP 分析全部显示为 0**（其余如用户、令牌数据正常）。

**无需任何额外操作**：上面的一键脚本 / `deploy.sh` 会自动检测 NewAPI 是否启用了 `LOG_SQL_DSN`，若启用则自动解析、做容器名 / 网络改写、写入工具 `.env` 并把工具容器接入日志库网络。NewAPI 未启用时则跳过（日志查询回落主库，行为不变）。

```bash
# 一键脚本已涵盖日志库；重新运行即可让已部署实例补上日志库连接
bash <(curl -sSL https://raw.githubusercontent.com/james-6-23/new_api_tools/main/install.sh)
```

> 单独修复 / 不想整体重部署时，也可只跑日志库脚本：
> ```bash
> bash <(curl -sSL https://raw.githubusercontent.com/james-6-23/new_api_tools/main/setup-log-db.sh)         # 检测并配置
> bash <(curl -sSL https://raw.githubusercontent.com/james-6-23/new_api_tools/main/setup-log-db.sh) --print # 仅预览，不改动
> ```
> 即使日志库一时连不上，后端也只会**降级为读主库**（日志暂时为空），不会崩溃。

## 配置说明

推荐优先使用 `SQL_DSN` 配置完整数据库连接串；设置了 `SQL_DSN` 后，分离式 `DB_*` 配置会作为兼容兜底。

| 变量名 | 说明 | 示例/默认值 |
|---|---|---|
| `FRONTEND_PORT` | 对外访问端口 | `1145` |
| `FRONTEND_BIND` | 端口绑定网卡；生产反代时建议绑定本机 | `0.0.0.0` / `127.0.0.1` |
| `ADMIN_PASSWORD` | 管理后台登录密码 | 必填 |
| `API_KEY` | 前后端内部 API Key | 部署脚本自动生成 |
| `JWT_SECRET` | JWT 签名密钥 | 部署脚本自动生成 |
| `JWT_EXPIRE_HOURS` | JWT 过期时间（小时） | `24` |
| `SQL_DSN` | 推荐的完整数据库连接串 | `host=... port=5432 user=...` |
| `LOG_SQL_DSN` | 日志专用库连接串（支持 MySQL、PostgreSQL 和 ClickHouse；留空则日志查询回落主库）。建议用 `setup-log-db.sh` 自动生成 | `clickhouse://user:pass@host:9000/logs` / 可选 |
| `DB_ENGINE` | 兼容旧版分离配置的数据库类型 | `postgres` / `mysql` |
| `DB_DNS` | 数据库主机或容器服务名 | `postgres` |
| `DB_PORT` | 数据库端口 | `5432` / `3306` |
| `DB_NAME` | 数据库名称 | `new-api` |
| `DB_USER` | 数据库用户名 | `postgres` |
| `DB_PASSWORD` | 数据库密码 | 必填 |
| `DB_MAX_OPEN_CONNS` | 数据库最大打开连接数 | `50` |
| `DB_MAX_IDLE_CONNS` | 数据库最大空闲连接数 | `15` |
| `NEWAPI_NETWORK` | NewAPI 所在 Docker 网络 | `new-api_default` |
| `NEWAPI_BASEURL` | NewAPI 内部地址，毛利解析和封禁/解封经它调用网关管理接口 | 毛利解析、封禁必填 |
| `NEWAPI_API_KEY` | new-api 管理员访问令牌：毛利页读取权威成本基准和价格簿，封禁/解封调用 `POST /api/user/manage` | 毛利解析、封禁必填 |
| `REDIS_HOST` | Redis 容器主机名；接入网关网络时避免使用会撞名的 `redis` | `beat-newapi-tools-redis` |
| `REDIS_PORT` | Redis 端口 | `6379` |
| `REDIS_PASSWORD` | 内置 Redis 密码 | 留空或自定义 |
| `TIMEZONE` | 服务时区 | `Asia/Shanghai` |
| `LOG_LEVEL` | 日志级别 | `info` |
| `DOWNLOAD_GEOIP` | 部署脚本是否下载 GeoIP（IP 定位用，约 70MB；可选，默认交互询问且默认跳过） | `0` 跳过 / `1` 下载 |
| `SKIP_GEOIP_DOWNLOAD` | 设为 `1` 时强制跳过 GeoIP 下载 | 可选 |

## 本地开发

后端：

```bash
cd backend
go mod download
go run ./cmd/server
```

前端：

```bash
cd frontend
npm install
npm run dev
```

## API 端点

主要端点分组：

| 分组 | 端点 |
|---|---|
| 健康检查 | `GET /api/health`、`GET /api/health/db` |
| 认证 | `POST /api/auth/login`、`POST /api/auth/logout` |
| 仪表盘 | `GET /api/dashboard/growth`、`GET /api/dashboard/growth/trend`；经营视图 `GET /api/dashboard/business/{finance,conversion,gifts-risk,tasks,pricing-gaps}?window=today\|7d\|30d`、`GET /api/dashboard/business/supply` |
| 毛利分析 | `GET /api/margin-analysis`、`GET /api/margin-analysis/pricing[?refresh=true]`，核算实际收入、供应商成本、赠额成本、内部成本和全量定价场景；定价接口 45 秒内读不完网关返回 504，有上次结果时返回 200 + `stale: true` |
| IP 分布 | `GET /api/dashboard/ip-distribution?window=1h\|6h\|24h\|7d`，按国家/地区 |
| 充值 | `GET /api/top-ups`、`GET /api/top-ups/analytics/*` |
| 用户分析 | `GET /api/risk/users/:id/analysis`、`GET /api/ip/lookup/:ip`、`GET /api/ip/geo/*` |
| 模型状态 | `/api/model-status/*`（需登录；`status/batch`、`status/multiple` 单次最多 100 个模型） |
| 来源分析 | `GET /api/acquisition/overview?days=30`（`days=0` 为全部时间） |
| 用户与令牌 | `GET /api/users`、`GET /api/users/groups`、`GET /api/users/login-sources`、`POST /api/users/:id/ban`、`POST /api/users/:id/unban`（转调网关）、`GET /api/tokens`、`GET /api/tokens/statistics`、`POST /api/tokens/batch-disable`、`POST /api/tokens/batch-enable`、`GET /api/tokens/audit` |
| 渠道监控 | `GET /api/channels/{overview,log-stats,ability-matrix,model-health,error-analysis}` |
| 存储与系统 | `GET /api/storage/*`、`GET /api/system/*` |

## 数据来源说明

本项目依赖 NewAPI 既有数据结构。涉及 NewAPI 数据访问、字段含义、列类型和索引时，应优先参考仓库内的真实生产库导出：

```text
pgsql_schema_export_20260505/
```

其中 `structure.txt` 用于快速确认表和列，`schema.sql` 用于查看完整建表、索引和默认值。`new-api/` 是上游 NewAPI 源码的只读参考目录，不应在本项目提交对它的改动。

## 贡献与支持

欢迎提交 Issue 和 Pull Request。改动数据库查询时，请同时确认 PostgreSQL / MySQL 的 SQL 差异，并避免对 NewAPI 原表结构做侵入式变更。

## License

MIT License

## Star History

[![Star History Chart](https://star-history.dera.page/svg?repos=james-6-23/new_api_tools&type=Date)](https://star-history.dera.page/#james-6-23/new_api_tools&Date)
