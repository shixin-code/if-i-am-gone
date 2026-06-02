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
  - 范围：不依赖进程连续运行，按当前时间、月度确认日与阶段时间戳推导应处阶段。
  - 证据：`shouldSendMonthlyCheckin`、`reminderDaysSince`、`TestDowntimeReplay`、`TestDowntimeDoesNotOverwriteOutstandingCheckin`、`TestMonthlyReminderProgression`。

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

- `[已完成]` 投递失败后的阶段推进策略修正。
  - 当前行为：单个或多个受益人投递失败时会记录 `FAILED`，并保持当前阶段等待后续 tick 重试；已成功投递的受益人不会重复发送。
  - 实现策略：每个阶段必须全部受益人对应投递记录为成功后才推进到下一阶段。
  - 涉及文件：`internal/scheduler/scheduler.go`、`internal/scheduler/scheduler_test.go`。
  - 验证：`go test ./internal/scheduler -run TestStageWaitsForFailedDeliveriesBeforeProgressing -v`、`go test ./...` 通过。

- `[已完成]` 真实 Telegram 联调任务。
  - 验证结果：`getMe` 成功，真实 chat_id `sendMessage` 成功，本地应用发送确认按钮成功，点击按钮后 Scheduler 接受回调并重置为 `ALIVE`。
  - 验证时间：2026-06-01。
  - 证据：`data/state/app.log` 中出现 `用户已确认，状态重置为 ALIVE`；`state.db` 当前状态为 `ALIVE`、`miss_count=0`、`pending_token` 已清空。
  - 备注：非授权 chat_id 忽略与旧 token 失效尚未做真实人工攻击演练，已有代码路径与单元测试覆盖 token 防重放。

- `[已完成]` 真实 SMTP 联调任务。
  - 验证结果：使用 `config.yaml` 中的 SMTP 配置和 `.env` 中的授权码，纯文本测试邮件发送成功，附件测试邮件发送成功。
  - 验证时间：2026-06-01。
  - 证据：命令输出 `smtp_send=ok`、`smtp_attachment_send=ok`。
  - 备注：请在收件箱中人工确认中文标题/正文和 `ifgone-smtp-smoke.txt` 附件显示正常。

- `[部分完成]` 端到端快速节奏演练。
  - 当前情况：已补充目标流程快速节奏手工演练 checklist，明确准备、主链、取消路径、投递失败重试、下载链接验收和记录模板；已新增 `ifgonectl drill advance-checkin` 作为受控测试时间推进工具，避免手工改 SQLite；自动化 e2e 已覆盖主链与取消路径。真实 Telegram/SMTP/self_hosted 或 S3 环境的人工演练尚未执行。
  - 目标：用分钟级配置跑完整流程，覆盖打包、确认、最终提醒、预警、密码、文件、确认取消。
  - 产物：手工演练 checklist 或自动化集成测试。
  - 证据：`docs/quick-flow-drill.md`、`internal/app/e2e_test.go`、`cmd/ifgonectl/main.go`。
  - 验证：`TestDrillAdvanceCheckinShiftsCheckinTime`、`go test ./internal/app ./internal/scheduler ./cmd/ifgonectl`、`go test ./...` 通过。

- `[部分完成]` Docker build 与容器内运行验证。
  - 当前情况：已新增 `.dockerignore`，避免 `.env`、`config.yaml`、`data/`、数据库、归档、日志和构建产物进入 Docker build 上下文；已新增 `scripts/docker-smoke.sh` 使用临时假配置/假数据做安全 smoke；`docker-compose config` 可解析 compose 文件。2026-06-02 再次执行 `./scripts/docker-smoke.sh`，仍卡在 Docker daemon 拉取 Docker Hub 基础镜像超时，尚未完成容器实际启动验证。
  - 目标：执行 `docker compose up -d --build`，确认容器能启动、挂载路径可读写、日志输出正常。
  - 验证：`bash -n scripts/docker-smoke.sh` 通过；`docker-compose config` 可解析；`./scripts/docker-smoke.sh` 失败于 `registry-1.docker.io/v2/` 超时。
  - 安全备注：`docker-compose config` 会展开本地 `.env`，不要把完整输出提交或粘贴到公开记录。
  - 涉及文件：`Dockerfile`、`docker-compose.yml`、`.dockerignore`、`scripts/docker-smoke.sh`、`docs/deploy-checklist.md`。

- `[已完成]` 配置校验增强。
  - 当前情况：已补充 `target_flow`、下载链接有效期/次数、`self_hosted.public_base_url`、`self_hosted.listen_port`、SMTP port/账号/密码/from_email、受益人 name/email/lang、S3 必填项与预签名有效期、运行时 `source_dir` 可读和 `state_dir` 可写校验。
  - 目标：启动前尽早暴露危险配置。
  - 验证：`TestValidateRejectsInvalidSMTPBeneficiariesAndDownloadURLs`、`TestValidateRejectsIncompleteS3Config`、`TestValidateRuntimePaths`、`go test ./...` 通过。
  - 涉及文件：`internal/config/config.go`、`internal/config/config_test.go`、`cmd/ifgone/main.go`。

- `[已完成]` 文案资源化与模板补齐。
  - 当前情况：月度确认、确认按钮、确认成功/过期/错误回调、连续提醒、阶段提醒、触发流程取消提示、心跳、预提醒邮件、密码邮件、下载链接邮件等目标流程文案已接入 `templates.zh`；代码中仅保留防空配置的安全 fallback。
  - 目标：先迁移到 `config.example.yaml` 的中文模板并补齐中文版本；英文版本后续按需补充。
  - 验证：`TestNotifierTelegramTemplates`、`TestCallbackReplyUsesConfiguredTemplate`、`TestSendCheckinSendsInlineButton`、`TestNotifierEmailTemplates`、`TestRenderSupportsBraceAndAnglePlaceholders`、`go test ./...` 通过。
  - 涉及文件：`internal/app/notifier.go`、`internal/scheduler/scheduler.go`、`internal/telegram/bot.go`、`cmd/ifgone/main.go`、`config.example.yaml`。

- `[已完成]` 邮件模板缺失时的安全兜底。
  - 当前情况：配置加载时会校验 `templates.zh` 的关键 Telegram 与邮件模板，避免发送空标题或空正文。
  - 目标：为关键邮件模板提供默认值或配置校验。
  - 验证：`TestValidateRequiresCriticalZhTemplates`、`go test ./...` 通过。
  - 涉及文件：`internal/config/config.go`、`internal/app/notifier.go`。

- `[已完成]` 日志文件打开失败时显式告警。
  - 当前情况：日志目录创建失败或日志文件打开失败时，会在 stdout 输出一次明确警告并继续回退 stdout。
  - 目标：至少输出一次可见警告，避免误以为日志已落盘。
  - 验证：`TestSetupLoggerWarnsWhenLogFileCannotBeOpened`、`go test ./...` 通过。
  - 涉及文件：`cmd/ifgone/main.go`。

- `[已完成]` `download.mode` 与当前能力的一致性处理。
  - 当前情况：`self_hosted` 与 `s3` 均已实现下载链接链路；`self_hosted` 使用本机下载 token，`s3` 上传归档后生成对象存储预签名链接。
  - 目标：在统一下载链接实现前，启动时提示或限制下载模式，避免用户误配置。
  - 验证：`TestCreateLinkStoresTokenAndBuildsURL`、`TestCreateS3LinkUploadsAndPresignsWithoutLocalToken`、`go test ./...` 通过。
  - 涉及文件：`internal/config/config.go`、`internal/app/notifier.go`、`internal/download/service.go`、`internal/download/s3.go`。

## 目标流程改造：月度确认到下载链接投递

- `[已完成]` 目标流程文档留底。
  - 范围：每月固定日期 Telegram 确认、连续提醒、受益人预提醒、密码阶段打包、统一下载链接投递、阶段 Telegram 提醒、建议文案。
  - 证据：`docs/telegram-to-delivery-flow.md`。
  - 备注：目标流程主链已由代码实现；真实外部服务联调见独立待验证任务。

- `[已完成]` 目标流程文档参数化与自洽收口。
  - 当前情况：流程文档已将连续提醒期、D8/D11/D18 示例节点、密码/下载等待时长改为配置驱动表达；已修正过期的“当前实现差异”，并明确预提醒邮件中的下载日期是预估值、密码邮件中的下载日期按实际密码邮件成功时间重新计算。
  - 目标：将 `daily_reminder_days` 从固定 7 天表述收口为“默认 7 天，可配置”；将 D11/D18 标题改为示例或阶段名；将 `{password_delay_text}`、`{file_delay_text}` 调整为可表达小时/天的自然语言时长占位符；确保流程文档与当前实现状态不互相矛盾。
  - 证据：`docs/telegram-to-delivery-flow.md`。
  - 涉及文件：`docs/telegram-to-delivery-flow.md`。

- `[已完成]` README 与示例配置同步目标流程。
  - 当前情况：README 和 `config.example.yaml` 已补充目标流程说明；旧 `intervals` 字段已清理，目标流程统一使用 `target_flow`。
  - 目标：同步公开文档与示例配置到“月度确认、连续提醒、密码阶段打包、统一下载链接投递”的目标流程，同时保留当前实现状态说明，避免用户误配置。
  - 证据：`README.md`、`config.example.yaml`。
  - 涉及文件：`README.md`、`config.example.yaml`、`.env.example`。

- `[已完成]` 清理旧 `intervals` 兼容配置字段。
  - 当前情况：已从配置结构、默认值来源、校验逻辑、示例配置、README、Docker smoke 与测试配置中移除旧 `intervals` 字段；目标流程统一使用 `target_flow`。
  - 目标：移除 `pack_interval`、`checkin_interval`、`miss_threshold`、`final_grace`、`password_delay`、`file_delay` 等旧配置入口，避免新旧节奏并存造成误解。
  - 涉及文件：`internal/config/config.go`、`internal/config/config_test.go`、`config.example.yaml`、`README.md`、`scripts/docker-smoke.sh`。

- `[已完成]` 月度确认调度配置。
  - 当前情况：代码已新增 `target_flow.checkin_day_of_month`、`target_flow.daily_reminder_days`、`target_flow.timezone`、`target_flow.password_delay_after_warn`、`target_flow.file_delay_after_password`，并由 Scheduler 使用目标流程节奏。
  - 目标：新增或迁移到 `checkin_day_of_month`、`daily_reminder_days` 等配置，支持每月固定日期发送本月安全确认。
  - 验证：`TestMonthlyReminderProgression`、`TestConfirmDoesNotRepeatMonthlyCheckinInSameMonth`、`TestDowntimeDoesNotOverwriteOutstandingCheckin`、`go test ./...` 通过。
  - 涉及文件：`internal/config/config.go`、`config.example.yaml`、`internal/scheduler/scheduler.go`。

- `[已完成]` 连续提醒阶段。
  - 当前情况：D0 未确认后按 `daily_reminder_days` 每天最多发送 1 次 Telegram 连续提醒，最后一天使用最后提醒文案；连续提醒期结束后进入 `PENDING_TRIGGER`。
  - 目标：D0 未确认后按 `daily_reminder_days` 连续发送 Telegram 提醒；第 1-6 天使用普通提醒，第 7 天或最后一天使用最后连续提醒。
  - 文案：`安全确认提醒：系统已<N>天没收到你的确认。`
  - 验证：`TestMonthlyReminderProgression`、`go test ./...` 通过。
  - 涉及文件：`internal/scheduler/scheduler.go`、`internal/app/notifier.go`、`config.example.yaml`。

- `[已完成]` 确认回调文案与阶段取消提示资源化。
  - 当前情况：D0 点击成功回调、过期 token 提示、处理错误提示、确认按钮文案、触发流程取消提示均已纳入 `templates.zh`，示例配置提供中文默认文案。
  - 目标：将 D0 成功回调、触发流程暂停提示、过期 token 提示统一纳入模板或配置。
  - 验证：`TestCallbackReplyUsesConfiguredTemplate`、`TestCallbackReplyFallsBackWhenTemplateMissing`、`TestNotifierTelegramTemplates`、`TestSendCheckinSendsInlineButton`、`go test ./...` 通过。
  - 涉及文件：`cmd/ifgone/main.go`、`internal/scheduler/scheduler.go`、`internal/app/notifier.go`、`internal/telegram/bot.go`、`config.example.yaml`。

- `[已完成]` 受益人预提醒阶段改造。
  - 当前情况：连续提醒结束后先给用户本人发送 Telegram 阶段提醒，再给受益人发送预提醒邮件；邮件模板使用目标流程变量。
  - 目标：D8 或连续提醒结束后，先给用户本人发送 Telegram 阶段提醒，再给受益人发送预提醒邮件；邮件中展示预计密码发送时间和预计下载链接发送时间。
  - 占位符：`{password_delay_text}`、`{password_send_date}`、`{file_delay_text}`、`{file_link_send_date}`。
  - 验证：`TestDowntimeReplay`、`TestStageWaitsForFailedDeliveriesBeforeProgressing`、`go test ./...` 通过。
  - 涉及文件：`internal/scheduler/scheduler.go`、`internal/app/notifier.go`、`internal/templates`、`config.example.yaml`。

- `[已完成]` 日期、时区与预计发送时间计算。
  - 当前情况：目标流程使用 `target_flow.timezone` 计算月度确认日与展示预计发送日期；预提醒邮件展示预计密码发送日期和预计下载链接发送日期，密码邮件按密码阶段实际时间重新计算预计下载日期。
  - 目标：定义预计日期使用的时区、日期格式、tick 延迟说明；`password_send_date` 基于预提醒邮件全部成功时间计算，`file_link_send_date` 基于密码邮件全部成功时间计算，避免投递失败重试导致预计日期偏早。
  - 验证：`TestMonthlyCheckinTimeClampsMonthEnd`、`TestDowntimeReplay`、`go test ./...` 通过。
  - 涉及文件：`internal/scheduler/scheduler.go`、`internal/app/notifier.go`、`internal/templates`、`config.example.yaml`。

- `[已完成]` 密码阶段才打包。
  - 当前情况：Scheduler 已移除公共周期打包逻辑，只在密码阶段到达且当前归档为空时打包；打包失败不发送密码，后续 tick 重试；触发流程中确认会清理本轮归档状态。
  - 目标：在密码阶段到达后才立即打包 `source_dir`，生成本轮专用 AES-256 ZIP 和随机密码；打包失败不发送密码，后续 tick 重试。
  - 关键规则：打包成功后，密码邮件重试不能重新打包，避免密码与最终下载文件不一致。
  - 验证：`TestNoPackBeforePasswordStage`、`TestConfirmDuringTriggerClearsArchiveForNextTrigger`、`TestDowntimeReplay`、`go test ./...` 通过。
  - 涉及文件：`internal/scheduler/scheduler.go`、`internal/packer/packer.go`、`internal/state/state.go`。

- `[已完成]` 密码邮件阶段改造。
  - 当前情况：密码邮件正文可注入本次解压密码、`{file_delay_text}`、`{file_link_send_date}`；所有受益人密码邮件全部成功后才进入下载链接等待阶段。
  - 目标：密码邮件正文包含本次解压密码、`{file_delay_text}`、`{file_link_send_date}`；所有受益人密码邮件全部成功后才进入下载链接等待阶段。
  - 验证：`TestDowntimeReplay`、`TestStageWaitsForFailedDeliveriesBeforeProgressing`、`go test ./...` 通过。
  - 涉及文件：`internal/app/notifier.go`、`internal/templates`、`config.example.yaml`、`internal/scheduler/scheduler.go`。

- `[已完成]` 阶段 Telegram 提醒。
  - 当前情况：受益人预提醒、密码邮件、下载链接邮件三个阶段前都会给用户发送 Telegram 阶段提醒，并用内部 deliveries 阶段记录保证同一阶段不重复提醒。
  - 目标：在受益人预提醒、密码邮件、下载链接邮件三个阶段前分别给用户发送 Telegram 提醒，并提示如一切正常请点击最新确认按钮暂停后续流程。
  - 验证：`TestDowntimeReplay`、`go test ./...` 通过。
  - 涉及文件：`internal/scheduler/scheduler.go`、`internal/app/notifier.go`、`config.example.yaml`。

- `[已完成]` 统一下载链接投递。
  - 当前情况：文件阶段已移除附件路线，不再判断压缩包大小；所有受益人均生成 self_hosted 或 S3-compatible 下载链接并通过邮件发送。
  - 目标：所有加密文件统一生成下载链接并通过邮件发送；移除按大小选择附件/链接的分支。
  - 验证：`TestNotifierDeliverFileUsesDownloadLinkWithoutAttachment`、`TestNotifierDeliverFileUsesS3PresignedLink`、`internal/download/service_test.go`、`go test ./...` 通过。
  - 关联任务：self_hosted 下载 token、HTTP 下载服务与安全加固见“迭代 2：状态保护与自托管下载”；S3-compatible 上传与预签名 URL 见“迭代 3：对象存储与外部可靠性”。
  - 涉及文件：`internal/app/notifier.go`、`internal/config/config.go`、`config.example.yaml`、`internal/scheduler/scheduler.go`、`internal/download/s3.go`。

- `[已完成]` 流程状态与幂等记录适配。
  - 当前情况：Scheduler 已将现有阶段语义适配为月度确认、连续提醒、预提醒、密码、下载链接等待与完成状态；受益人邮件和阶段 Telegram 提醒均使用 deliveries 幂等记录。启动时会对缺少目标流程必要前置条件的旧触发状态做安全归一化。
  - 目标：适配月度确认、连续提醒、预提醒、密码、下载链接等待与完成状态；每个受益人、每个阶段成功后不重复发送，失败项后续重试。
  - 验证：`TestDowntimeReplay`、`TestStageWaitsForFailedDeliveriesBeforeProgressing`、`TestConfirmDuringTriggerCancels`、`TestNormalizeForTargetFlowResetsUnsafeLegacyTriggerState`、`go test ./...` 通过。
  - 涉及文件：`internal/state/state.go`、`internal/scheduler/scheduler.go`、`cmd/ifgone/main.go`。

- `[已完成]` 旧 state 兼容与迁移策略。
  - 当前情况：已新增启动时目标流程状态归一化：未知 phase、缺少 `last_checkin_sent_at` 或 `pending_token` 的触发中旧状态、缺少归档信息的 `PASSWORD_SENT` 会重置为 `ALIVE`，清理触发时间戳、归档字段与 deliveries，并写 audit；看起来有效的目标流程状态会保留继续执行。
  - 目标：定义升级后旧 `GRACE/PENDING_TRIGGER/WARNED/PASSWORD_SENT/FILE_SENT` 如何兼容、重置或迁移；必要时提供启动提示、备份建议或一次性迁移逻辑。
  - 证据：`internal/state/state.go`、`cmd/ifgone/main.go`、`docs/state-migration.md`。
  - 验证：`TestNormalizeForTargetFlowResetsUnsafeLegacyTriggerState`、`TestNormalizeForTargetFlowKeepsValidTargetFlowState`、`TestNormalizeForTargetFlowResetsUnknownPhase`、`go test ./...` 通过。
  - 涉及文件：`internal/state/state.go`、`cmd/ifgone/main.go`、`docs/state-migration.md`。

- `[部分完成]` 目标流程端到端演练。
  - 当前情况：已用自动化端到端模拟覆盖 D0 确认、连续提醒、预提醒、密码阶段打包、统一下载链接、确认取消；已补充快速节奏手工演练 checklist 和真实联调 checklist；真实快速配置手工演练尚未执行。
  - 目标：用快速配置跑通 D0 确认、连续提醒、预提醒、密码阶段打包、统一下载链接、任意阶段确认取消、投递失败重试。
  - 产物：手工演练 checklist 或自动化集成测试。
  - 证据：`internal/app/e2e_test.go`、`docs/quick-flow-drill.md`、`docs/real-flow-integration-checklist.md`。
  - 关联文档：`docs/telegram-to-delivery-flow.md`。

- `[未完成]` 真实目标流程联调。
  - 目标：使用真实 Telegram、真实 SMTP 与 self_hosted 或 S3 下载端点跑通目标流程，确认用户本人收到各阶段 Telegram 提醒，受益人收到预提醒、密码、下载链接，并能实际下载 AES-256 ZIP。
  - 产物：联调 checklist、日志摘要、人工验收记录。
  - 当前情况：已新增真实联调 checklist，覆盖主链、取消路径、投递失败重试、self_hosted 下载、S3-compatible 下载和脱敏记录模板；真实外部服务验收尚未执行。
  - 证据：`docs/real-flow-integration-checklist.md`。
  - 备注：不得在任务文档中记录真实 token、授权码、chat_id 或完整敏感邮箱配置。

## 迭代 2：状态保护与自托管下载

- `[已完成]` 下载 token 数据结构。
  - 已有：`download_tokens` 表、创建/读取/计数/清理方法，下载链接生成时会创建高熵 token。
  - 验证：`TestCreateLinkStoresTokenAndBuildsURL`、`go test ./...` 通过。
  - 涉及文件：`internal/state/state.go`、`internal/download/service.go`。

- `[已完成]` self_hosted 下载端点。
  - 目标：提供 HTTP 服务，根据 token 下载对应加密包，校验过期时间和最大下载次数。
  - 当前情况：`cmd/ifgone/main.go` 在 `download.mode=self_hosted` 时启动下载服务；`internal/download/server.go` 校验 token、过期时间、下载次数、文件存在性并返回 ZIP。
  - 验证：`TestDownloadServerServesFileAndIncrementsCount`、`TestDownloadServerRejectsExpiredAndLimitExceeded`、`go test ./...` 通过。
  - 涉及文件：`cmd/ifgone/main.go`、`internal/app/notifier.go`、`internal/state/state.go`。

- `[已完成]` 下载链接生成与邮件模板接入。
  - 当前情况：文件阶段投递会生成下载 token 与公开 URL，并注入 `{url}`、`{expiry}`、`{max_downloads}` 发送下载链接邮件。
  - 目标：为统一下载链接投递提供 token 创建、URL 生成、模板变量注入能力；主投递逻辑见“目标流程改造：统一下载链接投递”。
  - 验证：`TestCreateLinkStoresTokenAndBuildsURL`、`go test ./...` 通过。
  - 涉及文件：`internal/app/notifier.go`、`internal/state/state.go`、`config.example.yaml`。

- `[已完成]` 下载 token 审计。
  - 目标：记录 token 创建、成功下载、过期拒绝、次数超限拒绝、文件缺失等事件。
  - 当前情况：下载 token 创建、下载成功、token 缺失、过期、次数超限、文件缺失等路径会写入 audit。
  - 验证：`internal/download/service_test.go`、`go test ./...` 通过。
  - 涉及文件：`internal/state/state.go`、`internal/download/server.go`。

- `[已完成]` 下载链接安全加固。
  - 目标：保证 token 具备足够随机强度；下载 URL 不暴露本地敏感路径；HTTP 响应设置安全 header；下载前校验过期时间、下载次数、目标文件存在性；拒绝时写审计日志。
  - 当前情况：token 使用 32 字节随机数；URL 仅暴露 token；下载前校验过期、次数、文件存在；响应设置 `X-Content-Type-Options` 与 `Cache-Control`；拒绝路径写审计。
  - 验证：`internal/download/service_test.go`、`go test ./...` 通过。
  - 涉及文件：`internal/download/server.go`、`internal/state/state.go`、`internal/app/notifier.go`。

- `[已完成]` state 中包密码字段加密。
  - 当前情况：启用 `state_protection.encrypt_password_field` 后，打包生成的归档密码会使用 scrypt 派生密钥 + AES-GCM 加密后保存到 `current_archive_password`；密码邮件投递前会解密为明文发送。
  - 目标：使用 `MASTER_PASSPHRASE` 派生密钥，加密保存压缩包密码，投递密码阶段再解密。
  - 验证：`TestEncryptedArchivePasswordStoredButDeliveredPlaintext`、`internal/secretbox/secretbox_test.go`、`go test ./...` 通过。
  - 涉及文件：`internal/scheduler/scheduler.go`、`internal/secretbox/secretbox.go`。

- `[已完成]` state 密码加密迁移兼容。
  - 目标：兼容已有明文 state，提供安全迁移或启动提示，避免升级后无法投递旧包密码。
  - 当前情况：解密逻辑通过密文版本前缀识别是否已加密；没有前缀的旧 `current_archive_password` 按明文兼容读取，避免升级后无法继续投递旧包。
  - 验证：`TestEncryptedArchivePasswordReadsLegacyPlaintext`、`TestDecryptPlaintextCompat`、`go test ./...` 通过。
  - 涉及文件：`internal/state/state.go`、配置加载与启动流程。

- `[已完成]` 迭代 2 单元测试与集成测试。
  - 当前情况：已覆盖下载 token 过期/次数限制、HTTP 下载成功、state 密码加解密、旧 state 明文兼容、统一下载链接邮件、目标流程端到端模拟、取消路径和 S3 预签名链接邮件路径。
  - 验证：`internal/download/service_test.go`、`internal/app/e2e_test.go`、`TestNotifierDeliverFileUsesS3PresignedLink`、`go test ./...` 通过。
  - 备注：真实 Telegram/SMTP/self_hosted/S3 联调属于独立待验证任务，不计入本地自动化测试完成度。

## 迭代 3：对象存储与外部可靠性

- `[已完成]` S3/OSS 配置结构。
  - 当前情况：`config.Download.S3`、`config.example.yaml` 示例字段、配置校验、客户端装配、上传、预签名 URL、错误处理与单元测试均已补齐。
  - 验证：`TestValidateRejectsIncompleteS3Config`、`TestCreateS3LinkUploadsAndPresignsWithoutLocalToken`、`go test ./...` 通过。

- `[已完成]` 对象存储上传与预签名 URL。
  - 当前情况：当 `download.mode=s3` 时，文件阶段会上传当前归档到 S3-compatible 对象存储，生成预签名下载链接并注入受益人下载邮件；S3 模式不创建本地下载 token，下载次数展示为“不限制”。
  - 目标：当 `download.mode=s3` 时上传归档并给受益人发送预签名下载链接。
  - 验证：`TestCreateS3LinkUploadsAndPresignsWithoutLocalToken`、`TestNotifierDeliverFileUsesS3PresignedLink`、`go test ./...` 通过。
  - 涉及文件：`internal/download/s3.go`、`internal/download/service.go`、`internal/app/notifier.go`、`config.example.yaml`、`README.md`。

- `[待验证]` 真实 S3/OSS 联调。
  - 目标：使用真实 S3-compatible 对象存储验证上传权限、对象 key、预签名链接公网可访问性、链接过期时间、测试对象清理和受益人下载体验。
  - 产物：联调 checklist、日志摘要、人工验收记录。
  - 当前情况：已新增 S3-compatible 联调步骤和脱敏记录模板；真实对象存储验收尚未执行。
  - 证据：`docs/real-flow-integration-checklist.md`。
  - 备注：不得在任务文档中记录真实 access key、secret key、bucket 私密路径或完整预签名链接。

- `[已完成]` 外部探活集成说明。
  - 当前情况：已补充 healthchecks.io、Uptime Kuma、VPS/下载端点/证书/Docker 状态监控建议，以及告警处理 checklist；已实现程序内主动 ping webhook，成功/失败均写审计，失败不影响核心投递。
  - 目标：补充 healthchecks.io / Uptime Kuma 的部署建议，或提供主动 ping webhook。
  - 证据：`docs/external-healthcheck.md`、`README.md`。
  - 验证：`internal/reliability/healthcheck_test.go`、`TestValidateRejectsInvalidHealthcheckConfig`、`go test ./...` 通过。
  - 涉及文件：`README.md`、`docs/external-healthcheck.md`、`internal/reliability/healthcheck.go`、`cmd/ifgone/main.go`、`config.example.yaml`。

- `[已完成]` HTTPS 反代部署文档。
  - 目标：说明 Nginx/Caddy 反代 self_hosted 下载端点，包含 TLS、安全 header、仅开放下载路径等。
  - 证据：`docs/https-reverse-proxy.md`。
  - 涉及文件：`README.md` 或 `docs/`。

- `[已完成]` 管理 CLI。
  - 当前情况：已新增 `cmd/ifgonectl`，支持 `status` 查看 state 摘要、`dry-run` 查看下一步动作提示、`cleanup-tokens` 清理过期下载 token、`test-email` 触发 SMTP 测试邮件、`pack --save-state` 手动打包并可写入 state、`drill advance-checkin` 辅助测试演练推进确认时间。
  - 目标：支持手动打包、查看状态、dry-run、触发测试邮件、清理过期 token，并提供安全的测试演练辅助能力。
  - 验证：`TestStatusAndDryRun`、`TestCleanupTokens`、`TestTestEmailBuildsAndSendsMessage`、`TestPackAndSaveState`、`TestDrillAdvanceCheckinShiftsCheckinTime`、`go test ./cmd/ifgonectl`、`go test ./...` 通过。
  - 涉及文件：`cmd/ifgonectl/main.go`、`cmd/ifgonectl/main_test.go`、`README.md`。

## 测试与质量

- `[已完成]` Scheduler 核心单元测试。
  - 覆盖：确认重置、防重放、漏回合推进、宕机重放、幂等、触发中取消、终态不打包。
  - 证据：`internal/scheduler/scheduler_test.go`。

- `[已完成]` Packer 核心单元测试。
  - 覆盖：打包读回、错误密码失败、7-Zip 兼容性条件测试、旧归档清理、密码唯一性。
  - 证据：`internal/packer/packer_test.go`。

- `[已完成]` Config 单元测试。
  - 目标：覆盖 env 展开、duration/bytes 解析、默认值、校验错误、语言回退。
  - 证据：`internal/config/config_test.go`。
  - 验证：`go test ./...` 通过。

- `[已完成]` State 单元测试。
  - 目标：覆盖初始行、保存读取、deliveries 幂等、download token 过期清理、时间解析错误。
  - 证据：`internal/state/state_test.go`。
  - 验证：`go test ./...` 通过。

- `[已完成]` Mailer MIME 构造测试。
  - 目标：验证中文 header 编码、附件边界、base64 折行、无附件纯文本格式。
  - 证据：`internal/mailer/mailer_test.go`。
  - 验证：`go test ./...` 通过。

- `[已完成]` Telegram API 客户端测试。
  - 目标：用 httptest 或可注入 endpoint/client 验证 sendMessage、getUpdates、callback filtering。
  - 证据：`internal/telegram/bot_test.go`。
  - 验证：`go test ./...` 通过。
  - 涉及文件：`internal/telegram/bot.go`、`internal/telegram/bot_test.go`。

- `[已完成]` 目标流程 Scheduler 单元测试。
  - 已覆盖：月度确认、同月确认防重复、跨月宕机不覆盖未确认流程、连续提醒、最后提醒、阶段 Telegram 提醒、密码阶段才打包、密码邮件成功后进入下载等待、任意阶段确认取消、投递失败重试与月末日期计算。
  - 补充覆盖：Scheduler 与真实 Notifier/下载服务组合后的目标流程端到端模拟。
  - 证据：`internal/scheduler/scheduler_test.go`、`internal/app/e2e_test.go`。

- `[已完成]` App Notifier 测试。
  - 目标：验证不同语言模板渲染、统一下载链接投递、owner alert。
  - 证据：`internal/app/notifier_test.go`。
  - 验证：`go test ./...` 通过。

- `[已完成]` 端到端模拟测试。
  - 目标：使用 fake Telegram/SMTP/Packer 或本地测试服务模拟完整流程，减少真实联调成本。
  - 证据：`internal/app/e2e_test.go`。
  - 验证：`go test ./...` 通过。

## 文档与运维

- `[已完成]` 基础 README。
  - 证据：`README.md`。

- `[已完成]` 首次部署 checklist。
  - 目标：逐项说明准备 Bot、获取 chat_id、SMTP 授权码、创建数据目录、试运行、恢复演练。
  - 证据：`docs/deploy-checklist.md`。

- `[已完成]` 灾难恢复说明。
  - 目标：说明如何从 `data/state`、归档文件、配置和 `.env` 恢复服务。
  - 证据：`docs/disaster-recovery.md`。

- `[已完成]` 安全说明。
  - 目标：说明 `.env`、`state.db`、归档、VPS 权限、备份、MASTER_PASSPHRASE 的风险和建议。
  - 证据：`docs/security-notes.md`。

- `[已完成]` 家人解压说明附件或模板。
  - 当前情况：已新增受益人下载与解压说明文档，覆盖邮件顺序、下载、Windows/macOS/Linux 推荐工具、常见问题与安全提醒；README 与邮件模板补充了文档引用。
  - 目标：给受益人提供更清晰的 7-Zip/WinRAR/Keka 操作说明，可作为邮件正文或附件。
  - 证据：`docs/beneficiary-extraction-guide.md`、`README.md`、`config.example.yaml`。

- `[已完成]` 版本发布与变更记录。
  - 当前情况：已新增版本发布与变更记录文档，按 MVP、目标流程改造、迭代 2、迭代 3、迁移注意事项、待验证项、后续迭代边界和发布前 checklist 梳理当前状态。
  - 目标：记录 MVP、迭代 2、迭代 3 的变更与迁移注意事项。
  - 证据：`docs/changelog.md`、`README.md`。

## 仓库整理

- `[已完成]` 清理未提交变更并按逻辑提交。
  - 当前情况：已用 `git status --short` 审核改动范围，排除 `.env`、`config.yaml`、`data/`、构建产物等敏感或运行产物后完成提交。
  - 目标：用 `git status --short` 审核改动范围，排除 `.env`、`config.yaml`、`data/`、构建产物等敏感或运行产物后，按逻辑生成 gitlog 并提交。
  - 验证：`go test -count=1 ./...`、`go build ./cmd/ifgone ./cmd/ifgonectl`、`git diff --check`、敏感扫描通过；提交 `17a2e1a feat(flow): 完善目标流程与下载链路`。

- `[已完成]` 确认 `.gitignore` 覆盖敏感与生成文件。
  - 当前情况：`.gitignore` 已覆盖 `.env`、`config.yaml`、`/data/`、数据库、ZIP 归档、根目录 `ifgone` 二进制和 `bin/`。`.code/supperpowers/` 未全局忽略，因为用户明确要求 `project-task-list.md` 可提交，后续提交时需按范围排除不需要的 superpowers 临时文件。
  - 目标：确保 `.env`、`config.yaml`、`data/`、构建产物、`.code/supperpowers/` 不进入版本库。
  - 证据：`.gitignore`、`git status --short`。

- `[已完成]` 清理构建产物。
  - 当前情况：已发现并删除根目录 `ifgone` 二进制；`.gitignore` 已忽略 `/ifgone` 和 `/bin/`。
  - 目标：本地执行 `go build ./cmd/ifgone` 可能生成根目录 `ifgone` 二进制；需确认是否存在并加入忽略或删除。
  - 证据：`find . -maxdepth 2 -type f \( -name 'ifgone' -o -name '*.zip' -o -name '*.db' -o -name '*.log' \) -print`。

## 后续更新规则

- 完成任务后，把对应标签从 `[未完成]` 或 `[部分完成]` 改为 `[已完成]`，并补充证据与验证命令。
- 如果任务实现方向变化，保留旧任务并追加备注，不直接删除历史任务。
- 新增功能前，先在本文件补充任务，再按逻辑分组逐项执行。
