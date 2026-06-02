# 旧 state.db 兼容与迁移策略

本文说明从旧流程升级到目标流程时，`state.db` 如何处理。目标流程已经改为“每月固定日期确认、连续提醒、受益人预提醒、密码阶段才打包、统一下载链接投递”，旧状态库中的部分阶段语义可能不再完全匹配。

## 升级前建议

升级或替换程序前，先备份运行状态：

```bash
cp -a data/state data/state.backup.$(date +%Y%m%d-%H%M%S)
cp config.yaml config.yaml.backup.$(date +%Y%m%d-%H%M%S)
cp .env .env.backup.$(date +%Y%m%d-%H%M%S)
```

如果使用 Docker，请先停止服务再备份，避免复制到写入中的数据库：

```bash
docker compose down
cp -a data/state data/state.backup.$(date +%Y%m%d-%H%M%S)
docker compose up -d
```

## 自动归一化规则

程序启动后会先执行一次目标流程兼容检查，然后才启动 Telegram polling 和 tick 主循环。

以下状态会被安全重置为 `ALIVE`：

- phase 是未知值，调度器无法识别。
- phase 处于 `GRACE`、`PENDING_TRIGGER`、`WARNED` 或 `PASSWORD_SENT`，但缺少 `last_checkin_sent_at`。
- phase 处于 `GRACE`、`PENDING_TRIGGER`、`WARNED` 或 `PASSWORD_SENT`，但缺少 `pending_token`。
- phase 是 `PASSWORD_SENT`，但缺少当前归档路径或归档密码。

重置时会同时清理：

- `miss_count`
- `pending_token`
- `final_warning_at`
- `warned_at`
- `password_sent_at`
- `file_sent_at`
- 当前归档路径、密码、SHA256、打包时间
- deliveries 幂等记录

并写入 audit 事件：

```text
target_flow_state_normalized
```

## 为什么要重置这些状态

目标流程允许用户在下载链接邮件投递前通过 Telegram 最新确认按钮暂停后续流程。如果旧状态库没有 `pending_token` 或 `last_checkin_sent_at`，用户可能已经没有有效按钮可点，但系统仍可能继续推进到受益人邮件阶段。

因此迁移策略优先选择“防误发”：遇到缺少目标流程必要证据的触发中状态，重置为 `ALIVE`，等待下一个月度确认重新开始。

## 哪些状态会保留

以下状态不会自动修改：

- `ALIVE`
- `FILE_SENT`
- `COMPLETED`
- 带有 `last_checkin_sent_at` 和 `pending_token` 的 `GRACE`、`PENDING_TRIGGER`、`WARNED`
- 带有 `last_checkin_sent_at`、`pending_token`、当前归档路径和归档密码的 `PASSWORD_SENT`

这些状态看起来仍符合目标流程的必要前置条件，程序会按当前状态继续执行。

## 升级后如何确认

查看日志：

```bash
docker compose logs -f
```

如果发生自动归一化，会看到类似日志：

```text
旧状态库已按目标流程归一化: WARNED -> ALIVE (missing_pending_token)
```

也可以查看 audit 表：

```bash
sqlite3 data/state/state.db \
  "select ts,event,detail from audit where event='target_flow_state_normalized' order by ts desc limit 5;"
```

## 手工处理建议

如果你确认旧状态已经真实触发并且需要继续投递，不建议直接修改数据库绕过迁移。更安全的做法是：

1. 先恢复备份。
2. 记录旧状态对应的邮件是否已经发出。
3. 用快速节奏配置重新演练一次目标流程。
4. 必要时手工通知受益人，而不是让不完整状态自动推进。

## 后续可改进项

管理 CLI 可用于本地排查和手工维护：

- 查看当前 phase、pending token 是否存在、归档是否存在。
- dry-run 显示下一次 tick 会做什么。
- 手工清理旧归档与过期下载 token。
- 触发测试邮件。

后续还可以继续增加：

- 手工确认“保留旧触发状态继续执行”。
