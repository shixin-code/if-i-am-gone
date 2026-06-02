# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> 协作约定（语言、交互、git commit 规范等）见 [AGENTS.md](AGENTS.md)。本文件聚焦构建命令与架构。

## 项目概述

`if-i-am-gone`（意外开关）是一个「死亡开关 / Dead Man's Switch」：用户长期未通过 Telegram 确认存活后，分阶段把 AES-256 加密的文件可靠地传递给指定受益人。Go 实现，纯 Go SQLite 驱动（无 CGO）。

## 常用命令

```bash
go build ./cmd/ifgone          # 主守护进程
go build ./cmd/ifgonectl       # 本地管理 CLI

go test ./...                  # 全部测试
go test ./internal/scheduler   # 单包测试（核心状态机）
go test ./internal/scheduler -run TestTick_Warned   # 单个测试

./ifgone --config config.yaml --tick 1m    # 运行；--tick 控制 tick 周期
```

- 测试覆盖了核心行为；`internal/packer` 的测试若环境装了 `7z` 会额外验证真实 7-Zip 兼容性。
- 快速演练目标流程：把 `config.yaml` 的 `target_flow` 改成短周期（如 `password_delay_after_warn: 1m`）并配 `--tick 10s`，详见 [docs/quick-flow-drill.md](docs/quick-flow-drill.md)。

## 架构

### 两大设计基石（理解全局的关键）

1. **时间戳纯函数调度**：调度器本身**无状态**。`scheduler.Tick(now)` 的所有决策只基于「当前时间 + 持久化在 `state.db` 的绝对 UTC 时间戳」，不依赖内存计数器或进程不间断运行。VPS 宕机几天恢复后，单次 tick 即可算出真实应处阶段并补做动作。修改调度逻辑时**必须保持这一点**——不要引入依赖进程连续运行的状态。

2. **幂等投递**：每个受益人每个阶段（`deliveries` 表）只投递一次。`scheduler.deliver()` 先查表已成功则跳过，进程/VPS 重启重放绝不重复发送。

### 状态机（internal/scheduler + internal/state）

`Tick` 按 `state.Phase` 分派。阶段单向推进，唯一回退入口是用户确认：

```
ALIVE/GRACE ──漏确认达阈值──> PENDING_TRIGGER ──发预警邮件──> WARNED
  ──(等 password_delay)──> PASSWORD_SENT ──(等 file_delay)──> FILE_SENT ──> COMPLETED

任意触发阶段，用户 Telegram 确认 ──> scheduler.Confirm() 立即重置回 ALIVE，
                                    清空时间戳 + deliveries 表
```

- 关键约束：阶段推进要求**所有**受益人该阶段都投递成功（`allDelivered`），否则停在原阶段下拍重试。
- 「目标流程不提前打包」：到 `WARNED → PASSWORD_SENT` 那一拍才 `doPack()` 生成本轮专用密码。

### 依赖装配与分层

`cmd/ifgone/main.go` 是组合根，装配后并发运行两个循环：tick 循环 + Telegram polling。

- `internal/scheduler` — 核心状态机，定义 `Notifier` / `Packer` 接口（依赖倒置，便于测试注入假实现）。**不要**让 scheduler 直接依赖具体 telegram/mailer。
- `internal/app` — 适配器层，把 `telegram` + `mailer` + `templates` + `download` 粘合为 scheduler 需要的 `Notifier`，把 `packer` 适配为 `Packer`。
- `internal/state` — SQLite（WAL 模式）持久化。单行 `state` 表 + `deliveries` / `download_tokens` / `audit` 表。所有时间存为 RFC3339 UTC 字符串；可空时间字段用 `*time.Time` 区分 NULL。
- `internal/config` — YAML 加载 + 校验，支持 `${ENV}` 占位符、时长（`Duration`）、字节阈值（`Bytes`）解析。
- `internal/secretbox` — 可选地用 `MASTER_PASSPHRASE` 加密 state.db 中的归档密码（`state_protection.encrypt_password_field`）。
- `internal/download` — 两种下载模式：`self_hosted`（本机 `/download/{token}`，带过期/次数/审计）与 `s3`（S3-compatible 预签名 URL）。
- `internal/packer` — 用 `github.com/yeka/zip` 生成 WinZip AES-256 加密 ZIP。
- `internal/reliability` — 心跳 + 外部探活 healthcheck（失败仅记日志/审计，不影响投递）。

### 启动时的状态归一化

`state.NormalizeForTargetFlow()` 在启动时保守地把不符合目标流程前置条件的旧/损坏状态（缺 pending token、缺 last_checkin、未知 phase）重置为 ALIVE，避免用户已无有效确认按钮却继续误投递。

## 配置与密钥

- 全部带注释的选项见 [config.example.yaml](config.example.yaml)。
- 敏感凭据（bot token、SMTP 密码、`MASTER_PASSPHRASE` 等）放 `.env`，配置里用 `${VAR}` 引用。
- `config.yaml`、`.env`、`data/` 不进版本控制，也被 `.dockerignore` 排除。

## 管理 CLI（ifgonectl）

不启动 Telegram polling、不主动发邮件。子命令：`status` / `dry-run` / `cleanup-tokens` / `test-email` / `pack [--save-state]` / `drill advance-checkin`。`pack --save-state` 与 `drill` 仅用于手工操作和测试演练。
