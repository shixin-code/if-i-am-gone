// Package telegram 用 Telegram Bot API 发送确认消息并通过 long polling 接收用户确认。
//
// 为避免引入第三方库（且我们的需求很窄：发带按钮的消息、轮询回调），
// 直接基于标准库 net/http 调用 Bot API。
//
// 防冒充三层：
//  1. 只接受配置的 chat_id 的回调，其余忽略并记审计；
//  2. inline button 的 callback_data 内嵌一次性 token，校验通过才算确认；
//  3. 确认后由调用方立即清 token + 重置时间戳（见 scheduler）。
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var apiBase = "https://api.telegram.org/bot"

// Bot 封装 token、目标 chat_id 与 HTTP 客户端。
type Bot struct {
	token  string
	chatID int64
	client *http.Client
	offset int64 // long polling 的 update offset
}

// New 创建一个 Bot。
func New(token string, chatID int64) *Bot {
	return &Bot{
		token:  token,
		chatID: chatID,
		client: &http.Client{Timeout: 65 * time.Second}, // 略大于 long poll timeout
	}
}

// callbackConfirmPrefix 是确认按钮 callback_data 的前缀，后接一次性 token。
const callbackConfirmPrefix = "confirm:"

// SendCheckin 发送一条带确认按钮的消息，按钮回调携带 token。
func (b *Bot) SendCheckin(text, buttonText, token string) error {
	if buttonText == "" {
		buttonText = "确认正常"
	}
	keyboard := map[string]any{
		"inline_keyboard": [][]map[string]string{{
			{"text": buttonText, "callback_data": callbackConfirmPrefix + token},
		}},
	}
	kbJSON, _ := json.Marshal(keyboard)
	_, err := b.call("sendMessage", url.Values{
		"chat_id":      {strconv.FormatInt(b.chatID, 10)},
		"text":         {text},
		"reply_markup": {string(kbJSON)},
	})
	return err
}

// SendMessage 发送一条纯文本消息（最后强提醒、心跳、取消通知等）。
func (b *Bot) SendMessage(text string) error {
	_, err := b.call("sendMessage", url.Values{
		"chat_id": {strconv.FormatInt(b.chatID, 10)},
		"text":    {text},
	})
	return err
}

// ConfirmEvent 表示一次合法的用户确认。
type ConfirmEvent struct {
	Token           string
	CallbackQueryID string
}

// PollConfirmations 用 long polling 拉取更新，过滤出来自正确 chat_id 的确认回调。
// onConfirm 回调返回是否「接受」（token 是否匹配）；据此给用户即时反馈。
// onSpoof 在收到非授权 chat_id 的回调时调用（用于审计）。
// 该方法阻塞运行直到 ctx 取消。
func (b *Bot) PollConfirmations(
	ctx context.Context,
	onConfirm func(ev ConfirmEvent) (accepted bool, reply string),
	onSpoof func(chatID int64, data string),
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := b.getUpdates(ctx)
		if err != nil {
			// 网络抖动等：短暂退避后重试，不退出。
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		for _, u := range updates {
			b.offset = u.UpdateID + 1
			if u.CallbackQuery == nil {
				continue
			}
			cq := u.CallbackQuery
			data := cq.Data
			fromChat := cq.Message.Chat.ID

			// 防冒充第一层：chat_id 白名单。
			if fromChat != b.chatID {
				if onSpoof != nil {
					onSpoof(fromChat, data)
				}
				b.answerCallback(cq.ID, "无权操作")
				continue
			}

			if len(data) > len(callbackConfirmPrefix) && data[:len(callbackConfirmPrefix)] == callbackConfirmPrefix {
				token := data[len(callbackConfirmPrefix):]
				accepted, reply := onConfirm(ConfirmEvent{Token: token, CallbackQueryID: cq.ID})
				_ = accepted
				b.answerCallback(cq.ID, reply)
			}
		}
	}
}

// --- Telegram API 类型（仅保留所需字段） ---

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

type update struct {
	UpdateID      int64          `json:"update_id"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type callbackQuery struct {
	ID      string  `json:"id"`
	Data    string  `json:"data"`
	Message message `json:"message"`
}

type message struct {
	Chat chat `json:"chat"`
}

type chat struct {
	ID int64 `json:"id"`
}

func (b *Bot) getUpdates(ctx context.Context) ([]update, error) {
	v := url.Values{
		"timeout": {"50"}, // long polling 50s
		"offset":  {strconv.FormatInt(b.offset, 10)},
	}
	raw, err := b.callCtx(ctx, "getUpdates", v)
	if err != nil {
		return nil, err
	}
	var updates []update
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (b *Bot) answerCallback(callbackID, text string) {
	_, _ = b.call("answerCallbackQuery", url.Values{
		"callback_query_id": {callbackID},
		"text":              {text},
	})
}

func (b *Bot) call(method string, v url.Values) (json.RawMessage, error) {
	return b.callCtx(context.Background(), method, v)
}

func (b *Bot) callCtx(ctx context.Context, method string, v url.Values) (json.RawMessage, error) {
	endpoint := apiBase + b.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("解析 Telegram 响应失败: %w (body=%s)", err, string(body))
	}
	if !ar.OK {
		return nil, fmt.Errorf("Telegram API %s 失败: %s", method, ar.Description)
	}
	return ar.Result, nil
}
