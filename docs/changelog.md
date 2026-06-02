# 版本发布与变更记录

更新时间：2026-06-02

本文记录项目当前主要能力、迁移注意事项和后续迭代边界。正式发版时可按此文档拆分 release notes。

## 当前版本快照

当前代码已从初始 MVP 演进为目标流程版本：

- 每月固定日期 Telegram 安全确认。
- 未确认后按配置连续提醒。
- 连续提醒结束后进入受益人预提醒、密码、下载链接三阶段。
- 平时不提前打包；到密码阶段才生成本轮 AES-256 加密 ZIP 和随机密码。
- 文件阶段统一发送下载链接，不再按压缩包大小走附件分支。
- self_hosted 下载服务、下载 token、过期/次数限制和审计已实现。
- S3-compatible 上传与预签名下载链接已实现，真实对象存储联调待执行。
- state 中归档密码可选加密保存，并兼容旧明文 state。
- 关键中文文案已集中到 `templates.zh`。

## MVP 变更

MVP 已具备以下能力：

- Go 命令行入口与主循环。
- YAML 配置加载、环境变量展开、duration/bytes 解析和默认值。
- SQLite 状态库，包含单行 state、deliveries、download_tokens、audit。
- Telegram 确认消息、inline button、chat_id 白名单和一次性 token 防重放。
- SMTP 纯文本邮件、中文 header、附件 MIME 构造。
- AES-256 加密 ZIP 打包、SHA256 记录和旧归档清理。
- Dockerfile 与 docker-compose 基础部署。
- README、部署 checklist、灾难恢复、安全说明等基础文档。

## 目标流程改造

目标流程相对 MVP 的主要变化：

- 调度从旧 `checkin_interval + miss_threshold` 迁移到 `target_flow.checkin_day_of_month + daily_reminder_days`。
- 预提醒、密码、下载链接三个阶段前都会给用户本人发送 Telegram 阶段提醒。
- 用户在下载链接邮件投递成功前任意阶段确认，都可暂停后续流程并回到正常状态。
- 密码阶段才打包，密码邮件重试不会重新打包，避免密码与文件不一致。
- 预提醒邮件展示密码发送日期和下载链接预估日期；密码邮件会按实际密码邮件成功时间重新计算下载链接发送日期。
- 加密文件统一通过下载链接发送。
- D0 成功回调文案为：`本月已确认，祝君安康！`。
- 连续提醒文案、阶段提醒、确认按钮、回调提示、心跳和邮件正文都已纳入中文模板。

## 迭代 2 变更

迭代 2 已完成的增强：

- self_hosted 下载端点。
- 下载 token 创建、校验、下载计数、过期清理。
- 下载 token 创建、成功下载、拒绝路径和文件缺失审计。
- HTTP 下载响应安全 header。
- state 中归档密码字段加密：scrypt 派生密钥 + AES-GCM。
- 旧明文归档密码兼容读取。
- 旧 state 兼容与目标流程归一化。
- 配置校验增强：SMTP、受益人、下载、S3、运行时路径等。
- Docker build 上下文安全：新增 `.dockerignore`，避免真实配置和数据进入镜像构建上下文。
- Docker smoke 脚本：使用临时假配置和假数据验证容器部署路径。
- 管理 CLI：查看状态、dry-run、清理过期 token、触发测试邮件、手动打包并可写入 state。
- 程序内主动外部探活 ping：可定时访问 healthchecks.io 等第三方 ping URL，成功/失败均写审计，失败不影响核心投递。

## 迭代 3 变更

迭代 3 已推进的增强：

- S3-compatible 客户端：支持自定义 endpoint、bucket、region、access key 和 secret key。
- 文件阶段在 `download.mode=s3` 时会上传本轮归档并生成预签名下载链接。
- S3 预签名链接不创建本地下载 token；下载次数由对象存储侧策略控制，邮件中展示为“不限制”。
- 已补充 S3 链路单元测试，覆盖上传/预签名调用、审计和邮件模板注入。

## 迁移注意事项

从旧流程升级到当前目标流程时：

- 升级前备份 `data/state/`、`config.yaml`、`.env`。
- 新配置应补充 `target_flow` 字段。
- 旧 `intervals` 字段仍保留兼容，但目标调度主流程已改用 `target_flow`。
- 如果启用 `state_protection.encrypt_password_field`，必须设置并离线备份 `MASTER_PASSPHRASE`。
- 启动时会对缺少目标流程必要字段的旧触发中 state 做安全归一化，避免误投递。
- 详细规则见 `docs/state-migration.md`。

## 当前待验证项

以下能力已有代码或脚本，但仍需要真实环境验证：

- Docker build 与容器内运行：2026-06-02 再次执行 `./scripts/docker-smoke.sh`，当前环境仍拉取 Docker Hub 基础镜像超时，容器实际启动尚未完成验证。
- 真实目标流程联调：需要真实 Telegram、SMTP、self_hosted 或 S3 下载端点和人工收件确认。
- 真实 S3-compatible 对象存储联调：需要验证上传权限、预签名链接公网可访问性、过期时间和受益人下载体验。
- 端到端快速节奏手工演练：自动化模拟已覆盖主链，手工演练 checklist 已补充，真实环境执行仍需人工完成。

真实联调步骤与脱敏记录模板见 `docs/real-flow-integration-checklist.md`。

## 后续迭代边界

后续建议按以下顺序推进：

1. 完成真实目标流程联调和 Docker 容器验证。
2. 完成真实 S3-compatible 对象存储联调。
3. 完成正式 release tag 与提交整理。

## 发布前 checklist

正式发布前建议确认：

- `go test ./...` 通过。
- `git diff --check` 通过。
- `scripts/docker-smoke.sh` 在可拉取基础镜像的环境中通过。
- `docker-compose config` 会展开 `.env`，检查时不要把完整输出提交或粘贴到公开记录。
- 按 `docs/quick-flow-drill.md` 完成一次测试环境快速节奏演练。
- 按 `docs/real-flow-integration-checklist.md` 完成真实 Telegram、SMTP 和下载链路验收。
- 如启用 `reliability.healthcheck`，确认第三方监控能收到 ping，并在超时后触发告警。
- `git status --short` 中没有 `.env`、`config.yaml`、`data/`、归档、数据库或构建产物。
- 真实 Telegram 确认、SMTP 邮件和至少一种下载链接模式通过人工验收。
- `docs/telegram-to-delivery-flow.md`、`README.md`、`config.example.yaml` 与实际行为一致。
