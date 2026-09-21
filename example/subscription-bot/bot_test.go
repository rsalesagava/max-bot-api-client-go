package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	maxbot "github.com/max-messenger/max-bot-api-client-go/v2"
	"github.com/max-messenger/max-bot-api-client-go/v2/model"
)

const (
	testToken       = "test-token"
	testChannelID   = int64(-70000000000007)
	testChannelLink = "https://max.ru/megastroy_diy"
	testCatalogURL  = "https://megastroy.com/catalog/groups?products_groups%5B%5D=22"
	testDialogID    = int64(182182182)
)

// fakeMax — минимальная эмуляция Max Bot API для тестов.
type fakeMax struct {
	t *testing.T

	mu       sync.Mutex
	members  map[int64]bool // подписчики канала
	sent     []sentMessage  // отправленные сообщения
	answers  []sentAnswer   // ответы на callback
	requests []string       // все запросы: "METHOD /path"

	chatByLinkStatus int // код ответа GET /chats/@name (0 — 200)
	membersStatus    int // код ответа GET /chats/{id}/members (0 — 200)
}

type sentMessage struct {
	UserID int64
	Body   model.NewMessageBody
}

type sentAnswer struct {
	CallbackID string
	Answer     model.CallbackAnswer
}

func newFakeMax(t *testing.T) *fakeMax {
	return &fakeMax{t: t, members: make(map[int64]bool)}
}

func (f *fakeMax) channelJSON() []byte {
	return []byte(fmt.Sprintf(`{"chat_id":%d,"type":"channel","status":"active","title":"МЕГАСТРОЙ",`+
		`"is_public":true,"link":%q,"participants_count":100}`, testChannelID, testChannelLink))
}

func (f *fakeMax) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"user_id":229229229,"first_name":"Bot","username":"test_bot","is_bot":true}`))
	})

	mux.HandleFunc("PATCH /me/commands", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"commands":[]}`))
	})

	mux.HandleFunc("GET /chats/{link}", func(w http.ResponseWriter, r *http.Request) {
		link := r.PathValue("link")
		if strings.HasPrefix(link, "@") {
			if f.chatByLinkStatus != 0 {
				w.WriteHeader(f.chatByLinkStatus)
				_, _ = w.Write([]byte(`{"code":"not.found","message":"chat not found"}`))

				return
			}
			if link != "@megastroy_diy" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"not.found","message":"chat not found"}`))

				return
			}
			_, _ = w.Write(f.channelJSON())

			return
		}

		id, err := strconv.ParseInt(link, 10, 64)
		if err != nil || id != testChannelID {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"chat.not.found","message":"chat not found"}`))

			return
		}
		_, _ = w.Write(f.channelJSON())
	})

	mux.HandleFunc("GET /chats/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		if f.membersStatus != 0 {
			w.WriteHeader(f.membersStatus)
			_, _ = w.Write([]byte(`{"code":"access.denied","message":"bot is not admin"}`))

			return
		}
		if r.PathValue("id") != strconv.FormatInt(testChannelID, 10) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"chat.not.found","message":"chat not found"}`))

			return
		}

		var members []map[string]any
		for _, raw := range strings.Split(r.URL.Query().Get("user_ids"), ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				continue
			}
			f.mu.Lock()
			isMember := f.members[id]
			f.mu.Unlock()
			if isMember {
				members = append(members, map[string]any{"user_id": id, "first_name": "user", "is_admin": false})
			}
		}
		if members == nil {
			members = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"members": members})
	})

	mux.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		var body model.NewMessageBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("decode message body: %v", err)
		}
		userID, _ := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
		if userID == 0 {
			f.t.Errorf("POST /messages without user_id: %s", r.URL.RawQuery)
		}

		f.mu.Lock()
		f.sent = append(f.sent, sentMessage{UserID: userID, Body: body})
		f.mu.Unlock()

		_, _ = w.Write([]byte(`{"message":{"body":{"mid":"mid.1","seq":1,"text":""}}}`))
	})

	mux.HandleFunc("POST /answers", func(w http.ResponseWriter, r *http.Request) {
		var answer model.CallbackAnswer
		if err := json.NewDecoder(r.Body).Decode(&answer); err != nil {
			f.t.Errorf("decode callback answer: %v", err)
		}

		f.mu.Lock()
		f.answers = append(f.answers, sentAnswer{CallbackID: r.URL.Query().Get("callback_id"), Answer: answer})
		f.mu.Unlock()

		_, _ = w.Write([]byte(`{"success":true}`))
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(maxbot.AuthorizationHeader) != testToken {
			f.t.Errorf("request %s %s without valid token", r.Method, r.URL.Path)
		}
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		mux.ServeHTTP(w, r)
	})
}

func (f *fakeMax) setMember(userID int64, isMember bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[userID] = isMember
}

func (f *fakeMax) sentMessages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]sentMessage(nil), f.sent...)
}

func (f *fakeMax) sentAnswers() []sentAnswer {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]sentAnswer(nil), f.answers...)
}

func (f *fakeMax) requestLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.requests...)
}

func (f *fakeMax) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent, f.answers, f.requests = nil, nil, nil
}

// newTestBot поднимает fake Max API и создаёт бота поверх него.
func newTestBot(t *testing.T, fake *fakeMax, cfg Config) (*Bot, *UserStore) {
	t.Helper()

	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	api, err := maxbot.NewApi(testToken, maxbot.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewApi: %v", err)
	}

	if cfg.Token == "" {
		cfg.Token = testToken
	}
	if cfg.ChannelUsername == "" {
		cfg.ChannelUsername = "megastroy_diy"
	}
	if cfg.CatalogURL == "" {
		cfg.CatalogURL = testCatalogURL
	}
	if cfg.UsersFile == "" {
		cfg.UsersFile = filepath.Join(t.TempDir(), "users.txt")
	}

	store, err := OpenUserStore(cfg.UsersFile)
	if err != nil {
		t.Fatalf("OpenUserStore: %v", err)
	}

	bot := NewBot(api, store, cfg)
	bot.Init(context.Background())

	return bot, store
}

func testUser(id int64) model.User {
	return model.User{UserID: id, FirstName: "Иван", LastName: "Петров", Username: "ivan" + strconv.FormatInt(id, 10)}
}

func botStartedUpdate(user model.User) model.Update {
	return model.Update{
		UpdateType: model.UpdateBotStarted,
		ChatID:     testDialogID,
		UserID:     user.UserID,
		User:       &user,
	}
}

func messageUpdate(user model.User, chatType model.ChatType, text string) model.Update {
	return model.Update{
		UpdateType: model.UpdateMessageCreated,
		ChatID:     testDialogID,
		UserID:     user.UserID,
		User:       &user,
		MessageID:  "mid.123",
		Message: &model.MessageUpdate{
			Recipient: model.Recipient{ChatID: testDialogID, ChatType: chatType},
			Sender:    model.Sender{UserID: user.UserID, FirstName: user.FirstName, IsBot: user.IsBot},
			Body:      model.MessageBody{Mid: "mid.123", Text: text},
		},
	}
}

func callbackUpdate(user model.User, payload string) model.Update {
	return model.Update{
		UpdateType: model.UpdateMessageCallback,
		ChatID:     testDialogID,
		UserID:     user.UserID,
		MessageID:  "mid.prompt",
		Message: &model.MessageUpdate{
			Recipient: model.Recipient{ChatID: testDialogID, ChatType: model.ChatTypeDialog, UserID: user.UserID},
			Body:      model.MessageBody{Mid: "mid.prompt"},
		},
		Callback: &model.Callback{CallbackID: "cb-" + strconv.FormatInt(user.UserID, 10), Payload: payload, User: user},
	}
}

func userAddedUpdate(chatID int64, user model.User) model.Update {
	return model.Update{
		UpdateType: model.UpdateUserAdded,
		ChatID:     chatID,
		UserID:     user.UserID,
		User:       &user,
		ChatProp:   &model.ChatProp{},
	}
}

// buttons возвращает кнопки inline-клавиатуры сообщения (по одной на ряд).
func buttons(t *testing.T, body model.NewMessageBody) []*model.Button {
	t.Helper()

	var res []*model.Button
	for _, a := range body.Attachments {
		if a.Type != model.AttachInlineKeyboard {
			continue
		}
		for _, row := range a.Payload.Buttons {
			res = append(res, row...)
		}
	}

	return res
}

func assertSubscribePrompt(t *testing.T, msg sentMessage, userID int64) {
	t.Helper()

	if msg.UserID != userID {
		t.Fatalf("message sent to user %d, want %d", msg.UserID, userID)
	}
	if !strings.Contains(msg.Body.Text, "подпишитесь на наш канал @megastroy_diy") {
		t.Fatalf("unexpected prompt text: %q", msg.Body.Text)
	}
	if !strings.Contains(msg.Body.Text, "Иван Петров") {
		t.Fatalf("prompt must greet the user by name: %q", msg.Body.Text)
	}

	btns := buttons(t, msg.Body)
	if len(btns) != 2 {
		t.Fatalf("prompt has %d buttons, want 2", len(btns))
	}
	if btns[0].Type != model.ButtonLink || btns[0].URL != testChannelLink {
		t.Fatalf("first button must link to the channel, got %+v", btns[0])
	}
	if btns[1].Type != model.ButtonCallback || btns[1].Payload != callbackCheckSubscription {
		t.Fatalf("second button must be the check callback, got %+v", btns[1])
	}
}

func assertCatalog(t *testing.T, body model.NewMessageBody) {
	t.Helper()

	if !strings.Contains(body.Text, "подборка") {
		t.Fatalf("unexpected catalog text: %q", body.Text)
	}
	btns := buttons(t, body)
	if len(btns) != 1 {
		t.Fatalf("catalog message has %d buttons, want 1", len(btns))
	}
	if btns[0].Type != model.ButtonLink || btns[0].URL != testCatalogURL {
		t.Fatalf("catalog button must link to the catalog, got %+v", btns[0])
	}
	if btns[0].Text != buttonCatalog {
		t.Fatalf("catalog button text = %q", btns[0].Text)
	}
}

func TestBotInitResolvesChannelByLink(t *testing.T) {
	fake := newFakeMax(t)
	bot, _ := newTestBot(t, fake, Config{})

	if got := bot.ChannelID(); got != testChannelID {
		t.Fatalf("ChannelID() = %d, want %d", got, testChannelID)
	}
	if got := bot.channelLinkURL(); got != testChannelLink {
		t.Fatalf("channel link = %q, want %q", got, testChannelLink)
	}

	joined := strings.Join(fake.requestLog(), "\n")
	if !strings.Contains(joined, "GET /chats/@megastroy_diy") {
		t.Fatalf("expected channel lookup by link, got requests:\n%s", joined)
	}
	if !strings.Contains(joined, "PATCH /me/commands") {
		t.Fatalf("expected bot commands registration, got requests:\n%s", joined)
	}
}

func TestBotInitUsesChannelIDFromConfig(t *testing.T) {
	fake := newFakeMax(t)
	fake.chatByLinkStatus = http.StatusNotFound // поиск по ссылке недоступен

	bot, _ := newTestBot(t, fake, Config{ChannelID: testChannelID})

	if got := bot.ChannelID(); got != testChannelID {
		t.Fatalf("ChannelID() = %d, want %d", got, testChannelID)
	}
	for _, req := range fake.requestLog() {
		if req == "GET /chats/@megastroy_diy" {
			t.Fatalf("channel must not be resolved by link when CHANNEL_ID is set")
		}
	}

	user := testUser(1)
	fake.setMember(user.UserID, true)
	bot.Handle(context.Background(), botStartedUpdate(user))

	sent := fake.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	assertCatalog(t, sent[0].Body)
}

func TestStartNotSubscribed(t *testing.T) {
	fake := newFakeMax(t)
	bot, store := newTestBot(t, fake, Config{})
	fake.reset()

	user := testUser(1)
	bot.Handle(context.Background(), botStartedUpdate(user))

	if !store.Has(user.UserID) {
		t.Fatalf("user must be written to the users file on start")
	}

	sent := fake.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	assertSubscribePrompt(t, sent[0], user.UserID)

	if len(fake.sentAnswers()) != 0 {
		t.Fatalf("no callback answers expected on start")
	}
}

func TestStartSubscribed(t *testing.T) {
	fake := newFakeMax(t)
	bot, store := newTestBot(t, fake, Config{})
	fake.reset()

	user := testUser(2)
	fake.setMember(user.UserID, true)

	bot.Handle(context.Background(), messageUpdate(user, model.ChatTypeDialog, "/start"))

	if !store.Has(user.UserID) {
		t.Fatalf("user must be written to the users file")
	}

	sent := fake.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	if sent[0].UserID != user.UserID {
		t.Fatalf("message sent to %d, want %d", sent[0].UserID, user.UserID)
	}
	if !strings.Contains(sent[0].Body.Text, "Спасибо за подписку на канал @megastroy_diy") {
		t.Fatalf("unexpected text: %q", sent[0].Body.Text)
	}
	assertCatalog(t, sent[0].Body)
}

func TestAnyTextTriggersCheck(t *testing.T) {
	fake := newFakeMax(t)
	bot, _ := newTestBot(t, fake, Config{})
	fake.reset()

	user := testUser(3)
	bot.Handle(context.Background(), messageUpdate(user, model.ChatTypeDialog, "привет, где подборка?"))

	sent := fake.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	assertSubscribePrompt(t, sent[0], user.UserID)
}

func TestUserRecordedOnce(t *testing.T) {
	fake := newFakeMax(t)
	bot, store := newTestBot(t, fake, Config{})

	user := testUser(4)
	for i := 0; i < 3; i++ {
		bot.Handle(context.Background(), botStartedUpdate(user))
		bot.Handle(context.Background(), messageUpdate(user, model.ChatTypeDialog, "/check"))
	}

	if got := store.Count(); got != 1 {
		t.Fatalf("store has %d users, want 1", got)
	}
}

func TestCallbackNotSubscribed(t *testing.T) {
	fake := newFakeMax(t)
	bot, _ := newTestBot(t, fake, Config{})
	fake.reset()

	user := testUser(5)
	bot.Handle(context.Background(), callbackUpdate(user, callbackCheckSubscription))

	if sent := fake.sentMessages(); len(sent) != 0 {
		t.Fatalf("no messages expected, got %d", len(sent))
	}

	answers := fake.sentAnswers()
	if len(answers) != 1 {
		t.Fatalf("got %d callback answers, want 1", len(answers))
	}
	if answers[0].CallbackID != "cb-5" {
		t.Fatalf("answer callback_id = %q", answers[0].CallbackID)
	}
	if answers[0].Answer.Message != nil {
		t.Fatalf("message must not be replaced when user is not subscribed")
	}
	if answers[0].Answer.Notification == nil || *answers[0].Answer.Notification != textNotSubscribedYet {
		t.Fatalf("unexpected notification: %v", answers[0].Answer.Notification)
	}
}

func TestCallbackSubscribed(t *testing.T) {
	fake := newFakeMax(t)
	bot, _ := newTestBot(t, fake, Config{})
	fake.reset()

	user := testUser(6)
	fake.setMember(user.UserID, true)
	bot.Handle(context.Background(), callbackUpdate(user, callbackCheckSubscription))

	if sent := fake.sentMessages(); len(sent) != 0 {
		t.Fatalf("catalog must be delivered via callback answer, but %d messages were sent", len(sent))
	}

	answers := fake.sentAnswers()
	if len(answers) != 1 {
		t.Fatalf("got %d callback answers, want 1", len(answers))
	}
	if answers[0].Answer.Message == nil {
		t.Fatalf("callback answer must replace the prompt with the catalog message")
	}
	assertCatalog(t, *answers[0].Answer.Message)
}

func TestCallbackWithUnknownPayloadIsIgnored(t *testing.T) {
	fake := newFakeMax(t)
	bot, _ := newTestBot(t, fake, Config{})
	fake.reset()

	bot.Handle(context.Background(), callbackUpdate(testUser(7), "something_else"))

	if reqs := fake.requestLog(); len(reqs) != 0 {
		t.Fatalf("no requests expected, got %v", reqs)
	}
}

func TestChannelPostsAndBotsAreIgnored(t *testing.T) {
	fake := newFakeMax(t)
	bot, store := newTestBot(t, fake, Config{})
	fake.reset()

	// Пост в канале, где бот — администратор.
	bot.Handle(context.Background(), messageUpdate(testUser(8), model.ChatTypeChannel, "Новая акция!"))
	// Сообщение в групповом чате.
	bot.Handle(context.Background(), messageUpdate(testUser(9), model.ChatTypeChat, "/start"))
	// Сообщение от другого бота.
	other := testUser(10)
	other.IsBot = true
	bot.Handle(context.Background(), messageUpdate(other, model.ChatTypeDialog, "/start"))

	if reqs := fake.requestLog(); len(reqs) != 0 {
		t.Fatalf("no requests expected, got %v", reqs)
	}
	if store.Count() != 0 {
		t.Fatalf("nobody should be recorded, got %d users", store.Count())
	}
}

func TestUserJoinedChannelGetsCatalog(t *testing.T) {
	fake := newFakeMax(t)
	bot, _ := newTestBot(t, fake, Config{})

	known := testUser(11)
	bot.Handle(context.Background(), botStartedUpdate(known)) // запускал бота, но не подписан
	fake.reset()

	// Подписался на канал.
	fake.setMember(known.UserID, true)
	bot.Handle(context.Background(), userAddedUpdate(testChannelID, known))

	sent := fake.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	if sent[0].UserID != known.UserID {
		t.Fatalf("message sent to %d, want %d", sent[0].UserID, known.UserID)
	}
	if !strings.Contains(sent[0].Body.Text, "Спасибо, что подписались") {
		t.Fatalf("unexpected text: %q", sent[0].Body.Text)
	}
	assertCatalog(t, sent[0].Body)
	fake.reset()

	// Пользователь, который бота не запускал, — писать ему нельзя.
	bot.Handle(context.Background(), userAddedUpdate(testChannelID, testUser(12)))
	// Вступление в другой чат.
	bot.Handle(context.Background(), userAddedUpdate(-1, known))
	// Добавили бота.
	someBot := testUser(13)
	someBot.IsBot = true
	bot.Handle(context.Background(), userAddedUpdate(testChannelID, someBot))

	if reqs := fake.requestLog(); len(reqs) != 0 {
		t.Fatalf("no requests expected, got %v", reqs)
	}
}

func TestBotAddedToChannelAdoptsIt(t *testing.T) {
	fake := newFakeMax(t)
	fake.chatByLinkStatus = http.StatusNotFound // канал по ссылке определить нельзя

	bot, _ := newTestBot(t, fake, Config{})
	if bot.ChannelID() != 0 {
		t.Fatalf("channel must be unresolved, got %d", bot.ChannelID())
	}

	// Пока канал неизвестен — пользователь получает сообщение об ошибке проверки.
	fake.reset()
	user := testUser(14)
	bot.Handle(context.Background(), botStartedUpdate(user))
	sent := fake.sentMessages()
	if len(sent) != 1 || sent[0].Body.Text != textCheckFailed {
		t.Fatalf("expected check-failed message, got %+v", sent)
	}

	// Бота добавили в наш канал — он узнаёт chat_id из события.
	bot.Handle(context.Background(), model.Update{UpdateType: model.UpdateBotAdded, ChatID: testChannelID, IsChannel: true})
	if got := bot.ChannelID(); got != testChannelID {
		t.Fatalf("ChannelID() = %d after bot_added, want %d", got, testChannelID)
	}

	fake.reset()
	fake.setMember(user.UserID, true)
	bot.Handle(context.Background(), botStartedUpdate(user))
	sent = fake.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	assertCatalog(t, sent[0].Body)
}

func TestMembersCheckFailure(t *testing.T) {
	fake := newFakeMax(t)
	bot, store := newTestBot(t, fake, Config{})
	fake.membersStatus = http.StatusForbidden
	fake.reset()

	user := testUser(15)
	bot.Handle(context.Background(), botStartedUpdate(user))

	if !store.Has(user.UserID) {
		t.Fatalf("user must be recorded even if the check failed")
	}
	sent := fake.sentMessages()
	if len(sent) != 1 || sent[0].Body.Text != textCheckFailed {
		t.Fatalf("expected check-failed message, got %+v", sent)
	}

	fake.reset()
	bot.Handle(context.Background(), callbackUpdate(user, callbackCheckSubscription))
	answers := fake.sentAnswers()
	if len(answers) != 1 || answers[0].Answer.Notification == nil || *answers[0].Answer.Notification != textCheckFailed {
		t.Fatalf("expected check-failed notification, got %+v", answers)
	}
}
