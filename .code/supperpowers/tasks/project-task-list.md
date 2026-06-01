# if-i-am-gone 项目任务总表

更新时间：2026-06-01

## 状态标签

- `[已完成]`：代码已实现，且当前可通过本地测试或构建验证。
- `[部分完成]`：已有基础代码、配置或数据结构，但关键行为尚未完整闭环。
- `[未完成]`：当前仓库未发现对应实现。
- `[待验证]`：代码可能已具备，但需要真实外部服务、部署环境或人工演练确认。

## MVP 核心闭环

- `[已完成]` 初始化 Go 项目模块与依赖管理。
  - 证据：`go.mod`、`go.sum` 已存在。
  - 验证：`go test ./...` 通过。

- `[已完成]` 提供命令行入口与主运行循环。
  - 范围：读取配置、初始化日志、打开状态库、装配 Telegram/SMTP/Packer/Scheduler、启动 Telegram polling 与 tick 循环。
  - 证据：`cmd/ifgone/main.go`。
  - 验证：`go build ./cmd/ifgone` 通过。

- `[已完成]` 配置文件加载、环境变量展开、默认值与基础校验。
  - 范围：YAML 解析、`${VAR}` 展开、duration 与 bytes 自定义解析、必要字段校验。
  - 证据：`internal/config/config.go`、`config.example.yaml`、`.env.example`。

- `[已完成]` SQLite 状态持久化。
  - 范围：`state` 单行状态表、`deliveries` 幂等表、`download_tokens` 预留表、`audit` 审计表、WAL 与 busy timeout。
  - 证据：`internal/state/state.go`。

- `[已完成]` 核心状态机调度。
  - 范围：`ALIVE`、`GRACE`、`PENDING_TRIGGER`、`WARNED`、`PASSWORD_SENT`、`FILE_SENT`、`COMPLETED`。
  - 证据：`internal/scheduler/scheduler.go`。
  - 验证：`internal/scheduler/scheduler_test.go`。

- `[已完成]` 基于绝对时间戳的漏确认判定。
  - 范围：不依赖进程连续运行，宕机恢复后按当前时间推导阶段。
  - 证据：`missedRounds` 与 `TestDowntimeReplay`。

- `[已完成]` 幂等投递控制。
  - 范围：同一受益人同一阶段成功投递后不重复发送，失败记录可被后续覆盖。
  - 证据：`deliveries` 表、`AlreadyDelivered`、`RecordDelivery`、`Scheduler.deliver`。

- `[已完成]` 用户确认后重置流程。
  - 范围：校验 pending token，重置状态为 `ALIVE`，清理触发阶段时间戳，触发流程中确认时清空投递记录。
  - 证据：`Scheduler.Confirm`。
  - 验证：`TestConfirmResetsToAlive`、`TestConfirmDuringTriggerCancels`。

- `[已完成]` AES-256 加密 ZIP 打包。
  - 范围：递归打包源目录、随机密码、SHA256、旧归档清理、7-Zip 兼容性测试条件支持。
  - 证据：`internal/packer/packer.go`、`internal/packer/packer_test.go`。

- `[已完成]` Telegram 确认消息与回调轮询。
  - 范围：发送 inline button、long polling、chat_id 白名单、callback token 转交 Scheduler 校验。
  - 证据：`internal/telegram/bot.go`。
  - 备注：真实 Bot API 仍需部署环境验证。

- `[已完成]` SMTP 邮件发送基础能力。
  - 范围：隐式 TLS、STARTTLS/SendMail 路径、纯文本邮件、附件邮件、MIME header 编码、附件 base64。
  - 证据：`internal/mailer/mailer.go`、`internal/mailer/mime.go`。
  - 备注：真实 SMTP 服务仍需联调验证。

- `[已完成]` 三阶段邮件投递的 MVP 附件路径。
  - 范围：预警邮件、密码邮件、小文件附件邮件。
  - 证据：`internal/app/notifier.go`。

- `[已完成]` 系统心跳能力。
  - 范围：按 `heartbeat_interval` 给用户发送 Telegram 心跳。
  - 证据：`Scheduler.maybeHeartbeat`、`Notifier.SendHeartbeat`。

- `[已完成]` Docker 部署基础。
  - 范围：多阶段 Dockerfile、docker compose 服务、env_file、volume、restart policy。
  - 证据：`Dockerfile`、`docker-compose.yml`。
  - 备注：Docker build 与 VPS 部署仍需环境验证。

- `[已完成]` README 使用说明与路线图。
  - 范围：项目说明、快速开始、Docker 部署、配置说明、测试说明、局限与路线图。
  - 证据：`README.md`。

## MVP 收尾与可靠性加固

- `[未完成]` 投递失败后的阶段推进策略修正。
  - 当前情况：单个或多个受益人投递失败时会记录 `FAILED`，但状态仍可能推进到下一阶段。
  - 目标：明确策略并实现。建议至少保证关键阶段全部 `OK` 后再推进，或引入可配置的重试/跳过策略。
  - 涉及文件：`internal/scheduler/scheduler.go`、`internal/scheduler/scheduler_test.go`。

- `[未完成]` 真实 Telegram 联调任务。
  - 目标：使用真实 Bot Token 和 chat_id 验证确认消息、按钮回调、非授权 chat_id 忽略、旧 token 失效。
  - 产物：联调记录或 README 操作步骤。

- `[未完成]` 真实 SMTP 联调任务。
  - 目标：验证 465 SSL 与 587 STARTTLS 中至少一种真实邮箱可发，确认中文标题、正文、附件在常见邮箱客户端显示正常。
  - 产物：联调记录或 README 操作步骤。

- `[未完成]` 端到端快速节奏演练。
  - 目标：用分钟级配置跑完整流程，覆盖打包、确认、最终提醒、预警、密码、文件、确认取消。
  - 产物：手工演练 checklist 或自动化集成测试。

- `[未完成]` Docker build 与容器内运行验证。
  - 目标：执行 `docker compose up -d --build`，确认容器能启动、挂载路径可读写、日志输出正常。
  - 涉及文件：`Dockerfile`、`docker-compose.yml`。

- `[未完成]` 配置校验增强。
  - 当前情况：只校验关键字段，未充分校验 SMTP port、beneficiary lang、download self_hosted/s3 必要字段、路径可访问性等。
  - 目标：启动前尽早暴露危险配置。
  - 涉及文件：`internal/config/config.go`、新增配置测试。

- `[未完成]` 文案资源化与双语模板补齐。
  - 当前情况：部分用户可见文本仍硬编码在代码中，如心跳、OwnerAlert、取消通知、大文件说明。
  - 目标：迁移到 `config.example.yaml` 的 `templates.zh/en`，并补齐英文版本。
  - 涉及文件：`internal/app/notifier.go`、`internal/scheduler/scheduler.go`、`config.example.yaml`。

- `[未完成]` 邮件模板缺失时的安全兜底。
  - 当前情况：部分模板为空时可能发送空标题或空正文。
  - 目标：为关键邮件模板提供默认值或配置校验。
  - 涉及文件：`internal/config/config.go`、`internal/app/notifier.go`。

- `[未完成]` 日志文件打开失败时显式告警。
  - 当前情况：日志文件创建失败会静默退回 stdout。
  - 目标：至少输出一次可见警告，避免误以为日志已落盘。
  - 涉及文件：`cmd/ifgone/main.go`。

- `[未完成]` `download.mode` 与当前能力的一致性处理。
  - 当前情况：配置允许 `self_hosted` 和 `s3`，但实际大文件下载链路未实现。
  - 目标：在 MVP 阶段启动时提示或限制大文件模式，避免用户误配置。
  - 涉及文件：`internal/config/config.go`、`internal/app/notifier.go`。

## 迭代 2：状态保护与自托管下载

- `[部分完成]` 下载 token 数据结构。
  - 已有：`download_tokens` 表、创建/读取/计数/清理方法。
  - 未完成：token 生成、下载 URL 生成、HTTP 下载服务、权限/过期/次数校验闭环。
  - 涉及文件：`internal/state/state.go`、待新增下载服务包。

- `[未完成]` self_hosted 下载端点。
  - 目标：提供 HTTP 服务，根据 token 下载对应加密包，校验过期时间和最大下载次数。
  - 建议模块：`internal/download/server.go`。
  - 涉及文件：`cmd/ifgone/main.go`、`internal/app/notifier.go`、`internal/state/state.go`。

- `[未完成]` 大文件邮件链接投递。
  - 当前情况：超过阈值时只发送“暂无法通过附件发送”的说明。
  - 目标：生成下载 token 与 URL，使用 `file_email_body_link` 模板发送下载链接。
  - 涉及文件：`internal/app/notifier.go`、`config.example.yaml`。

- `[未完成]` 下载 token 审计。
  - 目标：记录 token 创建、成功下载、过期拒绝、次数超限拒绝、文件缺失等事件。
  - 涉及文件：`internal/state/state.go`、待新增下载服务包。

- `[未完成]` state 中包密码字段加密。
  - 当前情况：`current_archive_password` 明文保存，配置项已预留。
  - 目标：使用 `MASTER_PASSPHRASE` 派生密钥，加密保存压缩包密码，投递密码阶段再解密。
  - 涉及文件：`internal/state/state.go`、`internal/scheduler/scheduler.go`、`internal/app/notifier.go`、待新增加密包。

- `[未完成]` state 密码加密迁移兼容。
  - 目标：兼容已有明文 state，提供安全迁移或启动提示，避免升级后无法投递旧包密码。
  - 涉及文件：`internal/state/state.go`、配置加载与启动流程。

- `[未完成]` 迭代 2 单元测试与集成测试。
  - 范围：下载 token 过期/次数限制、HTTP 下载成功、state 密码加解密、旧 state 兼容、大文件邮件链接。

## 迭代 3：对象存储与外部可靠性

- `[部分完成]` S3/OSS 配置结构。
  - 已有：`config.Download.S3` 与 `config.example.yaml` 示例字段。
  - 未完成：客户端、上传、预签名 URL、错误处理、测试。

- `[未完成]` 对象存储上传与预签名 URL。
  - 目标：当 `download.mode=s3` 时上传归档并给受益人发送预签名下载链接。
  - 建议模块：`internal/download/s3.go`。

- `[未完成]` 外部探活集成说明或自动配置。
  - 目标：补充 healthchecks.io / Uptime Kuma 的部署建议，或提供主动 ping webhook。
  - 涉及文件：`README.md`、`config.example.yaml`、可选新增可靠性模块。

- `[未完成]` HTTPS 反代部署文档。
  - 目标：说明 Nginx/Caddy 反代 self_hosted 下载端点，包含 TLS、安全 header、仅开放下载路径等。
  - 涉及文件：`README.md` 或 `docs/`。

- `[未完成]` 管理 CLI。
  - 目标：支持手动打包、查看状态、dry-run、触发测试邮件、清理过期 token。
  - 涉及文件：`cmd/ifgone/main.go` 或新增 `cmd/ifgonectl`。

## 测试与质量

- `[已完成]` Scheduler 核心单元测试。
  - 覆盖：确认重置、防重放、漏回合推进、宕机重放、幂等、触发中取消、终态不打包。
  - 证据：`internal/scheduler/scheduler_test.go`。

- `[已完成]` Packer 核心单元测试。
  - 覆盖：打包读回、错误密码失败、7-Zip 兼容性条件测试、旧归档清理、密码唯一性。
  - 证据：`internal/packer/packer_test.go`。

- `[未完成]` Config 单元测试。
  - 目标：覆盖 env 展开、duration/bytes 解析、默认值、校验错误、语言回退。
  - 建议文件：`internal/config/config_test.go`。

- `[未完成]` State 单元测试。
  - 目标：覆盖初始行、保存读取、deliveries 幂等、download token 过期清理、时间解析错误。
  - 建议文件：`internal/state/state_test.go`。

- `[未完成]` Mailer MIME 构造测试。
  - 目标：验证中文 header 编码、附件边界、base64 折行、无附件纯文本格式。
  - 建议文件：`internal/mailer/mailer_test.go`。

- `[未完成]` Telegram API 客户端测试。
  - 目标：用 httptest 或可注入 endpoint/client 验证 sendMessage、getUpdates、callback filtering。
  - 涉及文件：`internal/telegram/bot.go`、`internal/telegram/bot_test.go`。

- `[未完成]` App Notifier 测试。
  - 目标：验证不同语言模板渲染、小文件附件、大文件链接或说明邮件、owner alert。
  - 建议文件：`internal/app/notifier_test.go`。

- `[未完成]` 端到端模拟测试。
  - 目标：使用 fake Telegram/SMTP/Packer 或本地测试服务模拟完整流程，减少真实联调成本。

## 文档与运维

- `[已完成]` 基础 README。
  - 证据：`README.md`。

- `[未完成]` 首次部署 checklist。
  - 目标：逐项说明准备 Bot、获取 chat_id、SMTP 授权码、创建数据目录、试运行、恢复演练。
  - 建议文件：`docs/deploy-checklist.md` 或 README 章节。

- `[未完成]` 灾难恢复说明。
  - 目标：说明如何从 `data/state`、归档文件、配置和 `.env` 恢复服务。

- `[未完成]` 安全说明。
  - 目标：说明 `.env`、`state.db`、归档、VPS 权限、备份、MASTER_PASSPHRASE 的风险和建议。

- `[未完成]` 家人解压说明附件或模板。
  - 目标：给受益人提供更清晰的 7-Zip/WinRAR/Keka 操作说明，可作为邮件正文或附件。

- `[未完成]` 版本发布与变更记录。
  - 目标：记录 MVP、迭代 2、迭代 3 的变更与迁移注意事项。

## 仓库整理

- `[未完成]` 初始化 git commit。
  - 当前情况：`git status --short` 显示项目文件均为未跟踪。
  - 目标：确认忽略规则无误后进行首次提交。

- `[未完成]` 确认 `.gitignore` 覆盖敏感与生成文件。
  - 目标：确保 `.env`、`config.yaml`、`data/`、构建产物、`.code/supperpowers/` 不进入版本库。

- `[未完成]` 清理构建产物。
  - 当前情况：本地执行 `go build ./cmd/ifgone` 可能生成根目录 `ifgone` 二进制；需确认是否存在并加入忽略或删除。

## 后续更新规则

- 完成任务后，把对应标签从 `[未完成]` 或 `[部分完成]` 改为 `[已完成]`，并补充证据与验证命令。
- 如果任务实现方向变化，保留旧任务并追加备注，不直接删除历史任务。
- 新增功能前，先在本文件补充任务，再按逻辑分组逐项执行。
