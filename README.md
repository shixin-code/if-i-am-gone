# if-i-am-gone（意外开关）

一个「死亡开关 / Dead Man's Switch」应用：在你意外身亡或长期失能后，自动、分阶段地把加密信息可靠地传递给指定的家人。

## 它怎么工作

```
正常状态:
  [定时] 加密压缩你的文件 ──> [定时] Telegram 发确认 ──> 你点确认 ──> 重置计时，什么都不做

意外触发（你长期未确认）:
  连续多回合未确认 ──> 最后强提醒（Telegram + 给你本人发邮件）
       │ 仍未确认
       ▼
  阶段1: 给家人发「预警」邮件（告知 N 天后发密码、M 天后发文件）
       │
       ▼
  阶段2: 给家人发「解压密码」
       │
       ▼
  阶段3: 给家人发「加密压缩文件」（小走附件，大走下载链接）

  ⚠️ 任意阶段，只要你通过 Telegram 确认 ──> 立即取消所有后续动作，回到正常
```

## 设计要点

- **时间戳纯函数调度**：所有判定基于「当前时间 + 持久化的绝对时间戳」，不依赖进程不间断运行。VPS 宕机几天恢复后，单次 tick 即可算出真实应处阶段并补做动作。
- **幂等投递**：每个受益人每个阶段只投递一次，进程/VPS 重启重放绝不重复发送。
- **AES-256 加密 ZIP**：用 [`github.com/yeka/zip`](https://pkg.go.dev/github.com/yeka/zip) 生成 WinZip AES 标准加密包，家人用 **7-Zip / WinRAR** 即可解压。
- **防冒充**：Telegram 确认只接受配置的 `chat_id`，且按钮携带一次性 token，防重放。

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

测试用快速节奏：把 `config.yaml` 的 `intervals` 改成分钟级（如 `checkin_interval: 2m`、`miss_threshold: 2`、`final_grace: 1m`、`password_delay: 1m`、`file_delay: 1m`），配合 `--tick 10s`，几分钟就能走完整个流程。

## 部署（VPS + Docker）

```bash
cp config.example.yaml config.yaml && cp .env.example .env   # 编辑两者
mkdir -p data/source data/state
docker compose up -d --build
docker compose logs -f
```

`docker-compose.yml` 已配置 `restart: unless-stopped`，进程崩溃或 VPS 重启后自动拉起。

## 配置

见 [`config.example.yaml`](config.example.yaml) 全部带注释的选项。敏感凭据（bot token、SMTP 授权码等）放 `.env`，配置里用 `${VAR}` 占位符引用。

关键时间参数（默认=稳健节奏，从失联到真正发密码约 10 天缓冲）：

| 参数 | 含义 | 默认 |
|---|---|---|
| `checkin_interval` | 多久发一次确认 | 24h |
| `miss_threshold` | 连续漏几回合进入触发预备 | 5 |
| `final_grace` | 最后强提醒后多久才发预警邮件 | 48h |
| `password_delay` | 预警 → 发密码 | 72h |
| `file_delay` | 发密码 → 发文件 | 96h |

## 给家人的提示（重要）

加密包是 **AES-256 加密 ZIP**。Windows 资源管理器**自带**的解压**不支持** AES 加密 ZIP，必须用 **7-Zip**（免费，https://7-zip.org ）或 **WinRAR**。Mac 可用 **Keka**。家人会先收到密码邮件，再收到文件，用密码解压即可。

## 测试

```bash
go test ./...
```

`internal/scheduler/scheduler_test.go` 覆盖：确认重置、防重放、漏回合推进、**宕机重放后投递幂等**、触发流程中确认取消。
`internal/packer/packer_test.go` 验证 AES-256 ZIP 可读回；若环境装了 `7z`，还会验证 7-Zip 真实兼容性。

## 局限与可靠性

本系统依赖 VPS 持续在线 —— 而它要应对的恰是「主人不在了」。缓解手段：

- **系统心跳**：每 7 天给你发「系统正常运行中」。长期收不到 = VPS 可能挂了，请检查。
- 已开启 SQLite WAL 崩溃安全写、单拍 tick 异常隔离、`restart: unless-stopped`。
- **强烈建议**配合外部独立探活（如 healthchecks.io / Uptime Kuma）兜底「VPS 整体挂掉」的情况——届时连心跳都发不出，只有独立第三方能告警你。

## 路线图

- **MVP（当前）**：核心闭环 —— 配置/状态/加密打包/Telegram 确认/调度状态机/三阶段邮件（附件投递）。
- **迭代 2**：加密 state 中的密码字段（scrypt + AES）；大文件 self_hosted 下载端点；多语言模板完善。
- **迭代 3**：对象存储（S3/OSS）预签名 URL；外部探活集成；HTTPS 反代；管理 CLI（手动打包、查看状态、dry-run）。
