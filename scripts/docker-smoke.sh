#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${IMAGE:-if-i-am-gone:smoke}"
CONTAINER="${CONTAINER:-ifgone-smoke}"
HOST_PORT="${HOST_PORT:-18080}"
TMP_DIR="$(mktemp -d)"

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$TMP_DIR/source" "$TMP_DIR/state"
printf 'smoke test secret\n' > "$TMP_DIR/source/example.txt"

cat > "$TMP_DIR/config.yaml" <<'YAML'
source_dir: /data/source
state_dir: /data/state

intervals:
  pack_interval: 24h
  checkin_interval: 24h
  miss_threshold: 5
  final_grace: 48h
  password_delay: 72h
  file_delay: 96h

target_flow:
  checkin_day_of_month: 31
  daily_reminder_days: 7
  password_delay_after_warn: 72h
  file_delay_after_password: 168h
  timezone: Asia/Shanghai

archive:
  keep_archives: 3
  password_length: 32
  large_file_threshold: 20MB

telegram:
  bot_token: smoke-token
  chat_id: 123456789

smtp:
  host: smtp.example.com
  port: 465
  use_ssl: true
  username: smoke@example.com
  password: smoke-password
  from_name: "意外开关"
  from_email: smoke@example.com

beneficiaries:
  - {name: "测试受益人", email: test@example.com, lang: zh}

download:
  mode: self_hosted
  link_expiry: 336h
  max_downloads: 5
  self_hosted:
    public_base_url: http://127.0.0.1:18080
    listen_port: 8080

state_protection:
  encrypt_password_field: false
  master_passphrase: smoke-passphrase

reliability:
  heartbeat_enabled: false
  heartbeat_interval: 168h

logging:
  level: INFO
  file: /data/state/app.log

templates:
  zh:
    checkin_telegram: "本月安全确认：如果你一切正常，请点击下方按钮完成确认。"
    checkin_button_text: "确认正常"
    checkin_accepted_reply: "本月已确认，祝君安康！"
    checkin_expired_reply: "此确认已过期，请用最新的确认消息"
    checkin_error_reply: "处理出错，请稍后再试"
    daily_reminder_telegram: |
      安全确认提醒：系统已<N>天没收到你的确认。
      如果你一切正常，请点击最新确认消息中的“确认正常”按钮。
    final_reminder_telegram: |
      安全确认提醒：这是本轮连续提醒的最后一天。
      如果你一切正常，请尽快点击最新确认消息中的“确认正常”按钮。若仍未确认，系统将进入预设通知流程。
    warn_stage_telegram: |
      阶段提醒：系统即将向受益人发送预提醒邮件。
      如果你一切正常，请立即点击最新安全确认消息中的“确认正常”按钮，系统会暂停后续流程。
    password_stage_telegram: |
      阶段提醒：系统即将打包文件，并向受益人发送解压密码。
      如果你一切正常，请立即点击最新安全确认消息中的“确认正常”按钮，系统会暂停后续流程。
    file_stage_telegram: |
      阶段提醒：系统即将向受益人发送加密文件下载链接。
      如果你一切正常，请立即点击最新安全确认消息中的“确认正常”按钮，系统会暂停后续流程。
    cancel_flow_telegram: "本月已确认，后续流程已暂停。"
    heartbeat_telegram: "系统巡检正常：服务正在按计划运行。若长期收不到此消息，请检查服务器是否在线。"
    warn_email_subject: "[重要] 一封预定的信息"
    warn_email_body: "您好 {name}，预计在 {password_delay_text} 后即 {password_send_date} 发送密码，预计在密码发送后 {file_delay_text} 即 {file_link_send_date} 发送下载链接。"
    password_email_subject: "[重要] 解压密码"
    password_email_body: "您好 {name}，解压密码：{password}。预计在 {file_delay_text} 后即 {file_link_send_date} 收到下载链接。"
    file_email_subject: "[重要] 加密文件下载链接"
    file_email_body_link: "您好 {name}，下载链接：{url}，有效期：{expiry}，最多下载次数：{max_downloads}。"
YAML

docker build -t "$IMAGE" "$ROOT_DIR"
docker run -d \
  --name "$CONTAINER" \
  -p "127.0.0.1:${HOST_PORT}:8080" \
  -v "$TMP_DIR/config.yaml:/app/config.yaml:ro" \
  -v "$TMP_DIR/source:/data/source:ro" \
  -v "$TMP_DIR/state:/data/state" \
  "$IMAGE" --tick 10s >/dev/null

sleep 3
if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  docker logs "$CONTAINER" || true
  echo "容器未保持运行" >&2
  exit 1
fi

curl -fsS --max-time 5 "http://127.0.0.1:${HOST_PORT}/download/not-a-real-token" >/dev/null || true
test -f "$TMP_DIR/state/state.db"
test -f "$TMP_DIR/state/app.log"

echo "docker smoke ok"
