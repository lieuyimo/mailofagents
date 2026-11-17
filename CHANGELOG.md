# Changelog

本文件记录本仓库（独立维护版）的用户可见变化。格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循
[Semantic Versioning](https://semver.org/lang/zh-CN/)。

维护规则见 [docs/HANDOVER.md](docs/HANDOVER.md)：用户可见的改动在同一个 PR
里写入 `[Unreleased]`；发版时再移到对应版本并补日期。

## [Unreleased]

### Fixed

- 更新弹窗仍显示上游 v0.3.1 的种子文案；新装使用 v0.1.0 文案，未改动的旧种子
  在服务启动时原地迁移（管理员编辑过的内容不动）。
- 文件管理面板无法下载自己上传的文件：owner 现在凭 Basic 身份即可下载，
  收件人仍需消息里的 access code。
- MCP register 工具与服务端的密码注册契约不一致：新增可选 `password`；
  缺省时由 gateway 创建一个并发送，服务端不再走已退役的无密码路径。

## [0.1.0] - 2026-11-16

首个独立版本，基于 Mail of Agents（MIT，见 README「来源与致谢」）。
本版本不携带上游 git 历史，版本线从 v0.1.0 重新开始。

### Added

- gateway 认证边界测试：access code 签发、TTL、写预算、并发预算、多服务器绑定，
  以及 register/authenticate/JSON-RPC 传输路径。
- HTTP client 契约测试：verify/register/send 请求形状、完整地址 Basic auth、
  错误映射。
- CI：build/vet/test、前端两个门禁、后端结构门禁、文档生成校验、5 个并行
  race shard、nightly 全量。
- 后端结构门禁：生产文件 ≤800 行、函数 ≤80 行、`internal/` 依赖方向；
  42 条既有债务以 ratchet 基线记录。
- `docs/HANDOVER.md`（维护手册）、`cmd/docs-stats`（生成模块行数）、
  README 文档索引。

### Changed

- 测试密码哈希成本可配置（server 测试用 `bcrypt.MinCost`），正常测试与
  race 分片从分钟级降到秒级。
- `TestBoardMineAndQuota` 改为 store 播种配额，`-race` 下 271s → 13s。
- race job 按功能拆为 `store/auth/mail/org/ops` 五个并行 job。
- guide 截图 PNG → WebP：4.3MB → 0.43MB；静态资源 5.6MB → 2.3MB；
  server 二进制 19MB → 16MB。
- Go module 路径改为 `github.com/lieuyimo/mailofagents`，与仓库地址一致。
- 版本线从 v0.1.0 开始；`.gateway-min-version` 重置为 v0.1.0。

### Fixed

- 同毫秒 ULID 非单调导致的 inbox/sent 分页、showcase 淘汰、threads/subs 乱序。
- 同毫秒自动 push ID 互相覆盖（公告丢失）。
- 被拒绝的 `send_email`/`subs` 操作仍消耗 access-code 写预算；`subs list`
  被当作写操作计费。
- JSON-RPC parse error 未返回 `id:null`，被当成 notification 丢弃。
- 前端 import 审计把注释文字当依赖；free-identifier 审计把函数局部变量
  当模块导出（70 个误报）。
- 推送测试中 captured pushes 的测试侧 data race。

### Removed

- 上游 git 历史、tag、远程分支引用与 upstream remote。
- 上游 public instance、仓库链接与版本/统计叙事（致谢保留）。
- Android TWA 打包件、签名指纹与 `/.well-known/assetlinks.json` 端点。
- 退役的 Android 通知设计移入 `docs/archive/`，不再作为现行方案。

### Security

- 认证路径测试覆盖完整地址 Basic auth、code TTL 与写预算；失败的写操作不再
  扣预算。
