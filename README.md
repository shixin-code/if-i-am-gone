# if-i-am-gone（意外开关）

一个「死亡开关 / Dead Man's Switch」应用：在你意外身亡或长期失能后，自动、分阶段地把加密信息可靠地传递给指定的家人。

## 它怎么工作

```
正常状态:
  每月固定日期 Telegram 发本月安全确认 ──> 你点确认 ──> 等待下个月

意外触发（你长期未确认）:
  连续提醒期内仍未确认 ──> Telegram 阶段提醒 + 给受益人发预提醒邮件
       │ 仍未确认
       ▼
  阶段1: 按配置等待后，现场打包文件并给家人发「解压密码」
       │
       ▼
  阶段2: 再按配置等待后，给家人发「加密文件下载链接」

  ⚠️ 任意阶段，只要你通过 Telegram 确认 ──> 立即取消所有后续动作，回到正常
```

## 设计要点

- **时间戳纯函数调度**：所有判定基于「当前时间 + 持久化的绝对时间戳」，不依赖进程不间断运行。VPS 宕机几天恢复后，单次 tick 即可算出真实应处阶段并补做动作。
- **幂等投递**：每个受益人每个阶段只投递一次，进程/VPS 重启重放绝不重复发送。
- **AES-256 加密 ZIP**：用 [`github.com/yeka/zip`](https://pkg.go.dev/github.com/yeka/zip) 生成 WinZip AES 标准加密包，家人用 **7-Zip / WinRAR** 即可解压。
- **防冒充**：Telegram 确认只接受配置的 `chat_id`，且按钮携带一次性 token，防重放。
- **目标流程不提前打包**：平时不周期性生成最终投递包；到密码阶段才打包并生成本轮专用密码。
- **目标流程统一下载链接**：加密文件不再按大小区分附件或链接，可通过 self_hosted 或 S3-compatible 预签名链接发送。
- **state 密码加密**：可开启 `state_protection.encrypt_password_field`，用 `MASTER_PASSPHRASE` 加密保存在 `state.db` 中的归档密码。

## 快速开始（开发/本地）

```bash
cp config.example.yaml config.yaml   # 按需修改
cp .env.example .env                 # 填入 bot token、SMTP 授权码
# 准备待保护文件
mkdir -p data/source data/state
cp /path/to/your/secrets/* data/source/

go build ./cmd/ifgone
./ifgone --config config.yaml --tick 1m
```

测试用快速节奏：把 `config.yaml` 的 `target_flow` 改成短周期（如 `reminder_interval: 1m`、`reminder_count: 2`、`password_delay_after_warn: 1m`、`file_delay_after_password: 1m`），配合 `--tick 10s`，几分钟即可走完整个目标流程，无需任何辅助命令。月度确认日仍由 `checkin_day_of_month` 控制。
详细演练步骤见 [`docs/quick-flow-drill.md`](docs/quick-flow-drill.md)。
真实 Telegram、SMTP、self_hosted 或 S3-compatible 下载链路的最终验收见 [`docs/real-flow-integration-checklist.md`](docs/real-flow-integration-checklist.md)。

## 部署（推荐 VPS 原生 systemd）

```bash
go test ./...
go build -o ifgone ./cmd/ifgone
go build -o ifgonectl ./cmd/ifgonectl
```

推荐把二进制、配置、`.env` 和数据目录放到 `/opt/ifgone/`，再由 systemd 托管长期运行。完整步骤见 [`docs/native-systemd-deploy.md`](docs/native-systemd-deploy.md)，首次部署逐项检查见 [`docs/deploy-checklist.md`](docs/deploy-checklist.md)。

Docker 仍保留为可选部署/测试路径：

```bash
cp config.example.yaml config.yaml && cp .env.example .env   # 编辑两者
mkdir -p data/source data/state
docker compose up -d --build
docker compose logs -f
```

`docker-compose.yml` 已配置 `restart: unless-stopped`，进程崩溃或 VPS 重启后自动拉起。`.dockerignore` 会排除 `.env`、`config.yaml`、`data/`、数据库、归档和构建产物，避免真实敏感文件进入 Docker build 上下文。

## 配置

见 [`config.example.yaml`](config.example.yaml) 全部带注释的选项。敏感凭据（bot token、SMTP 授权码等）放 `.env`，配置里用 `${VAR}` 占位符引用。

关键时间参数：

目标流程使用：

| 参数 | 含义 | 默认示例 |
|---|---|---|
| `checkin_day_of_month` | 每月几号发送安全确认 | `1` |
| `send_time_of_day` | 月度确认与按天语义阶段的发送时间（本地时区 HH:MM） | `10:30` |
| `reminder_count` | 未确认后连续提醒几次 | `7` |
| `reminder_interval` | 两次提醒间隔（支持 Go duration，也支持整数天 `d`） | `1d` |
| `password_delay_after_warn` | 预提醒邮件成功后多久发送密码 | `3d` |
| `file_delay_after_password` | 密码邮件成功后多久发送下载链接 | `7d` |
| `timezone` | 月度确认日和预计日期展示时区 | `Asia/Shanghai` |

按天语义推荐写 `1d`、`3d`、`7d`；需要精确小时或分钟时继续写 `72h`、`90m` 这类 Go duration。
`send_time_of_day` 只作用于 `target_flow` 下的月度确认，以及 `reminder_interval` / `password_delay_after_warn` / `file_delay_after_password` 这些按天语义的 `d` 配置；像 `72h`、`90m` 仍按精确 duration 触发。

`state_protection.encrypt_password_field: true` 时，必须在 `.env` 配置 `MASTER_PASSPHRASE`。已有旧明文 state 可兼容读取；新打包产生的密码会以加密形式写入 `state.db`。

下载链接支持两种模式：

| 模式 | 说明 |
|---|---|
| `self_hosted` | 应用本机提供 `/download/{token}`，支持过期时间、最大下载次数和下载审计，需配置公网 HTTPS 反代。 |
| `s3` | 上传归档到 S3-compatible 对象存储，并发送预签名下载链接；适合 MinIO、Cloudflare R2、阿里 OSS S3 兼容端点等。 |

从旧流程升级到目标流程前，建议先备份 `data/state`、`config.yaml` 和 `.env`。程序启动时会自动检查旧 `state.db`，对缺少目标流程必要字段的触发中状态做安全归一化，避免用户没有有效确认按钮时继续误投递。规则见 [`docs/state-migration.md`](docs/state-migration.md)。

## 给家人的提示（重要）

加密包是 **AES-256 加密 ZIP**。Windows 资源管理器**自带**的解压**不支持** AES 加密 ZIP，必须用 **7-Zip**（免费，https://7-zip.org ）或 **WinRAR**。Mac 可用 **Keka**。目标流程中，家人会先收到密码邮件，再收到加密文件下载链接，用密码解压即可。更完整的操作说明见 [`docs/beneficiary-extraction-guide.md`](docs/beneficiary-extraction-guide.md)。

## 测试

```bash
go test ./...
```

`internal/scheduler/scheduler_test.go` 覆盖：确认重置、防重放、月度确认、连续提醒、阶段 Telegram 提醒、密码阶段才打包、投递幂等、触发流程中确认取消。
`internal/packer/packer_test.go` 验证 AES-256 ZIP 可读回；若环境装了 `7z`，还会验证 7-Zip 真实兼容性。
版本变更与发布前检查见 [`docs/changelog.md`](docs/changelog.md)。

## 管理 CLI

`ifgonectl` 提供本地管理能力，不启动 Telegram polling，也不会主动发送邮件：

```bash
go build ./cmd/ifgonectl
./ifgonectl status --config config.yaml
./ifgonectl dry-run --config config.yaml
./ifgonectl cleanup-tokens --config config.yaml
./ifgonectl test-email --config config.yaml --to you@example.com
./ifgonectl pack --config config.yaml --save-state
```

注意：`pack --save-state` 会把新归档路径、SHA256、密码和打包时间写入 `state.db`，仅在你明确需要手工打包时使用。
测试演练不再需要回拨时间戳的辅助命令——把 `target_flow.reminder_interval` 设为分钟级（如 `1m`）即可让连续提醒期分钟级自然推进。

## 局限与可靠性

本系统依赖 VPS 持续在线 —— 而它要应对的恰是「主人不在了」。缓解手段：

- **系统心跳**：默认关闭，避免 Telegram 文本消息过多；如需额外巡检提醒，可开启 `reliability.heartbeat_enabled`。
- **外部探活 ping**：可配置 `reliability.healthcheck`，让程序定时访问 healthchecks.io 等第三方 ping URL；失败只记录日志和审计，不影响核心投递。
- 已开启 SQLite WAL 崩溃安全写、单拍 tick 异常隔离；原生部署建议由 systemd `Restart=always` 托管，Docker 部署可使用 `restart: unless-stopped`。
- **强烈建议**配合外部独立探活（如 healthchecks.io / Uptime Kuma）兜底「VPS 整体挂掉」的情况——届时连心跳都发不出，只有独立第三方能告警你。配置建议见 [`docs/external-healthcheck.md`](docs/external-healthcheck.md)。

## 路线图

- **MVP / 目标流程 / 迭代 2（当前）**：配置、状态、加密打包、Telegram 确认、目标调度状态机、三阶段邮件、self_hosted 下载链接、state 密码加密、旧 state 迁移、关键文档与测试已完成。
- **迭代 3 已推进**：S3-compatible 上传与预签名 URL、管理 CLI 已实现；真实对象存储联调待执行。
- **待验证/收尾**：真实 systemd 部署验收、Docker 容器实际运行、真实目标流程联调、真实快速节奏手工演练、真实 S3/OSS 联调、正式发布与提交整理。
