package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type telegramRequest struct {
	Method string
	Form   url.Values
}

func withTelegramTestServer(t *testing.T, handler func(method string, form url.Values) any) (*Bot, *[]telegramRequest) {
	t.Helper()
	var mu sync.Mutex
	requests := []telegramRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/bottoken/"), "/")
		if len(parts) != 1 || parts[0] == "" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		form := cloneValues(r.Form)
		mu.Lock()
		requests = append(requests, telegramRequest{Method: parts[0], Form: form})
		mu.Unlock()
		resp := handler(parts[0], form)
		if resp == nil {
			resp = map[string]any{"ok": true, "result": true}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	oldBase := apiBase
	apiBase = srv.URL + "/bot"
	t.Cleanup(func() { apiBase = oldBase })

	bot := New("token", 123)
	bot.client = srv.Client()
	return bot, &requests
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

func TestSendCheckinSendsInlineButton(t *testing.T) {
	bot, requests := withTelegramTestServer(t, func(method string, form url.Values) any {
		return nil
	})

	if err := bot.SendCheckin("确认文本", "按钮文本", "tok123"); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 1 {
		t.Fatalf("请求数=%d", len(*requests))
	}
	req := (*requests)[0]
	if req.Method != "sendMessage" {
		t.Fatalf("method=%s", req.Method)
	}
	if req.Form.Get("chat_id") != "123" || req.Form.Get("text") != "确认文本" {
		t.Fatalf("form=%v", req.Form)
	}
	if !strings.Contains(req.Form.Get("reply_markup"), `"callback_data":"confirm:tok123"`) {
		t.Fatalf("reply_markup=%s", req.Form.Get("reply_markup"))
	}
	if !strings.Contains(req.Form.Get("reply_markup"), `"text":"按钮文本"`) {
		t.Fatalf("reply_markup=%s", req.Form.Get("reply_markup"))
	}
}

func TestGetUpdatesParsesAndUsesOffset(t *testing.T) {
	bot, requests := withTelegramTestServer(t, func(method string, form url.Values) any {
		if method != "getUpdates" {
			t.Fatalf("unexpected method=%s", method)
		}
		return map[string]any{
			"ok": true,
			"result": []map[string]any{{
				"update_id": 10,
				"callback_query": map[string]any{
					"id":      "cb1",
					"data":    "confirm:tok",
					"message": map[string]any{"chat": map[string]any{"id": 123}},
				},
			}},
		}
	})
	bot.offset = 7

	updates, err := bot.getUpdates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].UpdateID != 10 || updates[0].CallbackQuery.Data != "confirm:tok" {
		t.Fatalf("updates=%+v", updates)
	}
	if (*requests)[0].Form.Get("offset") != "7" || (*requests)[0].Form.Get("timeout") != "50" {
		t.Fatalf("form=%v", (*requests)[0].Form)
	}
}

func TestPollConfirmationsFiltersCallbacks(t *testing.T) {
	callCount := 0
	bot, requests := withTelegramTestServer(t, func(method string, form url.Values) any {
		callCount++
		switch method {
		case "getUpdates":
			if callCount > 1 {
				return map[string]any{"ok": true, "result": []any{}}
			}
			return map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 1,
						"callback_query": map[string]any{
							"id":      "bad",
							"data":    "confirm:bad-token",
							"message": map[string]any{"chat": map[string]any{"id": 999}},
						},
					},
					{
						"update_id": 2,
						"callback_query": map[string]any{
							"id":      "good",
							"data":    "confirm:good-token",
							"message": map[string]any{"chat": map[string]any{"id": 123}},
						},
					},
				},
			}
		case "answerCallbackQuery":
			return nil
		default:
			t.Fatalf("unexpected method=%s", method)
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gotConfirm := ""
	gotSpoof := int64(0)
	done := make(chan struct{})
	go func() {
		defer close(done)
		bot.PollConfirmations(ctx, func(ev ConfirmEvent) (bool, string) {
			gotConfirm = ev.Token
			cancel()
			return true, "确认成功"
		}, func(chatID int64, data string) {
			gotSpoof = chatID
		})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PollConfirmations 未退出")
	}
	if gotSpoof != 999 {
		t.Fatalf("spoof chatID=%d", gotSpoof)
	}
	if gotConfirm != "good-token" {
		t.Fatalf("confirm token=%q", gotConfirm)
	}
	answerTexts := []string{}
	for _, req := range *requests {
		if req.Method == "answerCallbackQuery" {
			answerTexts = append(answerTexts, req.Form.Get("text"))
		}
	}
	if len(answerTexts) != 2 || answerTexts[0] != "无权操作" || answerTexts[1] != "确认成功" {
		t.Fatalf("callback replies=%v requests=%+v", answerTexts, *requests)
	}
}

func TestCallReportsAPIError(t *testing.T) {
	bot, _ := withTelegramTestServer(t, func(method string, form url.Values) any {
		return map[string]any{"ok": false, "description": "bad request"}
	})
	err := bot.SendMessage("hello")
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("err=%v", err)
	}
}
