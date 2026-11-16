# 维护手册（HANDOVER）

本仓库是 Mail of Agents 的独立维护版本；来源与许可见 README「来源与致谢」。
本文只记录**工程规则**。架构见 [architecture.md](architecture.md)，隔离模型见
[isolation.md](isolation.md)，配置见 [config.md](config.md)。

## 仓库与模块

- Go module 仍是 `github.com/agentmail/agentmail`（为兼容保留；仓库名是
  `lieuyimo/mailofagents`）。
- 三个二进制：`agentmail-server`、`agentmail-gateway`、`agentmail-worker`。
- 数据是单个 bbolt 文件；前端零构建 ES module，内嵌在 server 二进制里。
- `main` 始终保持绿色；改动走分支 + PR + rebase 合并。

## 本地检查

```bash
go build ./...
go vet ./...
go test ./...
go test -race -count=1 ./internal/store
go test -race -count=1 ./internal/server   # 或按下面的分片跑
bash scripts/audit_frontend_imports.sh
python3 scripts/audit_free_identifiers.py
go run ./cmd/audit-structure
go run ./cmd/docs-stats -check
bash scripts/ci_race_shard.sh check
```

## CI

- 每次 push/PR：build、vet、test、前端/后端门禁、生成文档校验、分片覆盖校验。
- 5 个 race 分片并行：`store`、`auth`、`mail`、`org`、`ops`
  （定义在 `scripts/ci_race_shard.sh`）。
- nightly：同一套全量再跑一次，用于干净调度下的 flake 检查。
- 缓存 GOCACHE 与模块缓存；不使用 setup-go（本仓库曾持续遇到 429）。
- 新增的 server 测试必须恰好落进一个分片，否则 `check` 会让 CI 失败。

## 提交与评审

- 一个提交一个意图；标题用 `area: imperative summary`
  （`fix` / `feat` / `test` / `ci` / `docs` / `perf` / `chore`）。
- 提交正文说明行为变化与动机，不写内部调度暗号。
- 分支 → PR → CI 绿 → rebase 合并 → 删除分支；不直接改 `main`。
- 不 force-push 已共享历史。本仓库首次独立化时的历史重写是显式授权的例外。
- 不提交二进制、截图、日志；文档截图用 WebP，并保持内嵌（离线可用）。

## 测试约定

- 时间相关逻辑走可注入时钟；不要在测试里真实 `sleep`。
- server 测试打开 store 时用 `store.WithBcryptCost(bcrypt.MinCost)`；生产保持
  `bcrypt.DefaultCost`。
- 批量数据直接走 store 播种，只在边界走 HTTP——认证请求会跑 bcrypt。
- HTTP 契约用 `httptest`；认证、存储、协议路径必须有真实测试。
- `go test -race` 是默认门槛，不是可选项。

## 结构预算

- 生产文件 ≤ 800 行、函数 ≤ 80 行、`internal/` 依赖方向受控。
- `scripts/structure-baseline.txt` 是 ratchet：只许缩小或删除。缩小后运行
  `go run ./cmd/audit-structure -update` 刷新基线。
- 新增 `internal/` 依赖边必须先在 `cmd/audit-structure` 的 `allowedDeps`
  中声明。

## 文档

- 易变统计（模块行数等）由 `go run ./cmd/docs-stats -write` 生成，CI 用
  `-check` 拦截过期数字；不要手改生成块。
- 模块清单在 `docs/frontend-modules.tsv`；职责变化改清单，行数交给脚本。
- 架构、隔离、配置、部署、worker、boards 分开维护；README 有文档索引。

## 发布

- 只从绿色 `main` 打 `vMAJOR.MINOR.PATCH`；一个版本一个 tag，不累计 rc。
- tag 不可变；`.github/workflows/release.yml` 在 tag 推送时构建发布产物。
- 独立版本线从 `v0.1.0` 开始；`.gateway-min-version` 已重置为 `v0.1.0`。
- `.gateway-min-version` 只在 gateway 二进制实际发生变化时 bump，不跟随
  server 的版本号。
