# 目标流程快速节奏演练 Checklist

更新时间：2026-06-02

本文用于在本地或测试 VPS 上用分钟级配置演练目标流程。演练目标是确认：D0 确认、连续提醒、受益人预提醒、密码阶段打包、下载链接投递、任意阶段确认取消、投递失败重试等路径都符合 `docs/telegram-to-delivery-flow.md`。

## 安全边界

- 不要在本文或任务表中记录真实 Telegram token、SMTP 授权码、chat_id、完整收件人邮箱。
- 建议使用测试 Telegram bot、测试 SMTP 发件箱和测试受益人邮箱。
- 演练用 `data/source/` 只放测试文件，不放真实隐私资料。
- 演练前备份真实 `config.yaml`、`.env` 和 `data/state/`。

## 准备

```bash
cp config.example.yaml config.yaml
cp .env.example .env
mkdir -p data/source data/state
printf 'quick flow drill\n' > data/source/drill.txt
```

编辑 `.env`：

- 设置测试 `TELEGRAM_BOT_TOKEN`。
- 设置测试 `SMTP_PASSWORD`。
- 如果启用 state 密码加密，设置 `MASTER_PASSPHRASE`。

编辑 `config.yaml`：

- `telegram.chat_id` 改为测试 chat_id。
- `smtp.*` 改为测试邮箱配置。
- `beneficiaries` 改为测试收件人。
- `download.mode: self_hosted`。
- `download.self_hosted.public_base_url` 改为本机或测试 VPS 可访问地址。
- `target_flow.reminder_count: 2`。
- `target_flow.reminder_interval: 1m`。
- `target_flow.password_delay_after_warn: 1m`。
- `target_flow.file_delay_after_password: 1m`。
- `target_flow.checkin_day_of_month` 改为今天所在日期，或先用 `ifgonectl dry-run` 确认下一步动作。

如果只在本机验证邮件与状态，不验证公网下载，可先把 `public_base_url` 设置为 `http://127.0.0.1:8080`。

## 启动

```bash
go build ./cmd/ifgone ./cmd/ifgonectl
./ifgone --config config.yaml --tick 10s
```

另开一个终端观察状态：

```bash
./ifgonectl status --config config.yaml
./ifgonectl dry-run --config config.yaml
```

## 测试时间推进

连续提醒的节奏由 `target_flow.reminder_interval`（两次提醒间隔）和 `reminder_count`（提醒次数）共同决定。测试环境把 `reminder_interval` 设为分钟级（如 `1m`），配合 `--tick 10s`，连续提醒期会**按分钟自然推进**，无需任何回拨时间戳的辅助命令：

- D0 发送本月确认后，每过 `reminder_interval` 发一次提醒；
- 发满 `reminder_count` 次后，下一次 tick 进入 `PENDING_TRIGGER`，开始受益人通知流程。

例如 `reminder_count: 2` + `reminder_interval: 1m`，约 3 分钟即可从 D0 走到预提醒阶段。正式库请勿使用分钟级 interval。

## 主链演练

| 阶段 | 触发方式 | 预期结果 | 验收证据 |
|---|---|---|---|
| D0 安全确认 | 启动后到达本月确认日 | Telegram 收到“🟢 定时安全确认”和“✅ 一切正常”按钮 | Telegram 消息、`pending_token: <set>` |
| 连续提醒 | 不点击确认，每过 `reminder_interval` 一次 | Telegram 收到“🟡 安全确认提醒”和“✅ 一切正常”按钮，第 `reminder_count` 次收到“🔴 最后安全确认提醒” | Telegram 消息、`miss_count` 增加 |
| 受益人预提醒 | 连续提醒期结束后下一次 tick | 用户收到“⚠️ 阶段提醒 · 预提醒邮件”和“✅ 一切正常”按钮；受益人收到预提醒邮件 | 邮件主题 `[重要] 一封预定的信息`，state 进入 `WARNED` |
| 密码阶段 | 预提醒成功后等待 `password_delay_after_warn` | 系统现场打包；用户收到“🔐 阶段提醒 · 解压密码”；受益人收到解压密码邮件 | `data/state/archives/archive-*.zip`、密码邮件、state 进入 `PASSWORD_SENT` |
| 下载链接阶段 | 密码邮件成功后等待 `file_delay_after_password` | 用户收到“🔗 阶段提醒 · 下载链接”；受益人收到下载链接邮件 | 下载链接邮件、`download_tokens` 记录、state 进入 `FILE_SENT` |
| 完成 | 文件阶段成功后下一次 tick | 流程进入 `COMPLETED` | `ifgonectl status` 显示 `phase: COMPLETED` |


## 取消路径演练

在以下任一阶段点击 Telegram 最新“一切正常”按钮：

- 连续提醒阶段。
- 预提醒邮件已发送但密码阶段未到达。
- 密码邮件已发送但下载链接邮件未发送。

预期结果：

- Telegram 回调显示 `本月已确认，祝君安康！`。
- 触发流程取消提示发送给用户。
- state 回到 `ALIVE`。
- 后续 tick 不再发送下一阶段邮件。
- 若处于触发流程中，deliveries 会清空，下一次真实触发会重新走完整流程。

验证命令：

```bash
./ifgonectl status --config config.yaml
./ifgonectl dry-run --config config.yaml
```

## 投递失败重试演练

可临时把测试 SMTP 密码改错，观察投递失败后阶段是否停住：

1. 进入预提醒或密码阶段前，修改 `.env` 中的 SMTP 授权码为错误值。
2. 等待 tick 执行投递。
3. 确认 state 停留在当前阶段，不推进到下一阶段。
4. 恢复正确 SMTP 授权码并重启服务。
5. 确认后续 tick 只重试失败项，已成功项不重复发送。

验收证据：

- 日志出现投递失败。
- deliveries 中失败项可被后续成功覆盖。
- 阶段只有全部受益人成功后才推进。

## 下载链接验收

收到下载链接后：

```bash
curl -L -o /tmp/ifgone-drill.zip "下载链接"
```

预期：

- 第一次下载成功。
- 超过最大下载次数后拒绝。
- 超过有效期后拒绝。
- 解压时需要此前密码邮件中的密码。

## 自动化覆盖

当前自动化已覆盖：

- `TestTargetFlowEndToEndSimulation`：D0、连续提醒、预提醒、密码阶段打包、下载链接、COMPLETED。
- `TestTargetFlowEndToEndCancelBeforePassword`：预提醒后确认取消，确认不继续进入密码阶段。
- Scheduler 单元测试覆盖投递失败重试、阶段幂等、跨月宕机不覆盖旧 token 等细节。

运行：

```bash
go test ./internal/app ./internal/scheduler
```

## 记录模板

演练完成后建议记录：

```text
演练日期：
环境：本地 / 测试 VPS
Telegram：通过 / 未通过
SMTP：通过 / 未通过
self_hosted 下载：通过 / 未通过
取消路径：通过 / 未通过
投递失败重试：通过 / 未通过
备注：
```
