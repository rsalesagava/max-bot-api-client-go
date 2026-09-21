package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	maxbot "github.com/max-messenger/max-bot-api-client-go/v2"
	"github.com/max-messenger/max-bot-api-client-go/v2/model"
)

// callbackCheckSubscription — payload кнопки «Я подписался».
const callbackCheckSubscription = "check_subscription"

// Тексты сообщений бота.
const (
	textNeedSubscribe = "Привет, %s! 👋\n\n" +
		"Чтобы получить подборку товаров МЕГАСТРОЙ, подпишитесь на наш канал %s, " +
		"а затем нажмите кнопку «Я подписался»."
	textSubscribed = "Спасибо за подписку на канал %s! 🎉\n\n" +
		"Вот ваша подборка товаров — нажмите кнопку ниже, чтобы открыть её."
	textJustJoined = "Спасибо, что подписались на канал %s! 🎉\n\n" +
		"Как и обещали — подборка товаров по кнопке ниже."
	textNotSubscribedYet = "Подписка не найдена 🙁 Подпишитесь на канал и нажмите кнопку ещё раз."
	textCheckFailed      = "Не удалось проверить подписку 😔 Попробуйте ещё раз через минуту."

	buttonSubscribe = "📢 Подписаться на канал"
	buttonCheck     = "✅ Я подписался"
	buttonCatalog   = "🛒 Открыть подборку"
)

// Bot — бот проверки подписки на канал.
type Bot struct {
	api   *maxbot.Api
	store *UserStore
	cfg   Config

	mu           sync.Mutex
	channelID    int64
	channelLink  string
	channelTitle string
}

// NewBot создаёт бота. Если в конфигурации задан CHANNEL_ID, он используется сразу,
// иначе идентификатор канала определяется по его публичному имени при первом обращении.
func NewBot(api *maxbot.Api, store *UserStore, cfg Config) *Bot {
	return &Bot{
		api:         api,
		store:       store,
		cfg:         cfg,
		channelID:   cfg.ChannelID,
		channelLink: cfg.ChannelLink(),
	}
}

// Init выполняет подготовительные шаги: определяет канал и регистрирует команды бота.
// Ошибки здесь не фатальны — бот продолжит работу и повторит попытку позже.
func (b *Bot) Init(ctx context.Context) {
	if b.cfg.ChannelID != 0 {
		if chat, err := b.api.Chats.GetChat(ctx, b.cfg.ChannelID); err != nil {
			log.Printf("[warn] не удалось получить информацию о канале CHANNEL_ID=%d: %v "+
				"(проверьте, что бот добавлен в администраторы канала)", b.cfg.ChannelID, err)
		} else {
			b.setChannel(chat)
		}
	} else if _, err := b.resolveChannel(ctx); err != nil {
		log.Printf("[warn] %v", err)
		log.Printf("[warn] бот продолжит работу и повторит попытку при первом обращении пользователя. " +
			"Если ошибка повторяется — укажите числовой идентификатор канала в переменной CHANNEL_ID " +
			"(бот выводит его в лог при добавлении в канал: событие bot_added)")
	}

	_, err := b.api.Bots.PatchCommands(ctx, model.BotPatchCommands{
		Commands: []model.BotCommand{
			{Name: "start", Description: "Получить подборку товаров"},
			{Name: "check", Description: "Проверить подписку на канал"},
		},
	})
	if err != nil {
		log.Printf("[warn] не удалось зарегистрировать команды бота: %v", err)
	}
}

// Handle обрабатывает одно событие от Max.
func (b *Bot) Handle(ctx context.Context, u model.Update) {
	switch u.UpdateType {
	case model.UpdateBotStarted:
		// Пользователь нажал «Начать» в диалоге с ботом.
		b.onStart(ctx, u.GetUser())

	case model.UpdateMessageCreated:
		msg := u.GetMessage()
		// Реагируем только на личные сообщения: если бот — администратор канала,
		// ему приходят и посты канала, отвечать на них нельзя.
		if !isDialog(msg.Recipient) || msg.Sender.IsBot {
			return
		}
		b.onStart(ctx, u.GetUser())

	case model.UpdateMessageCallback:
		if u.GetCallbackPayload().Payload != callbackCheckSubscription {
			return
		}
		b.onCheckCallback(ctx, u.GetCallback())

	case model.UpdateBotAdded:
		if u.IsChannel {
			b.onAddedToChannel(ctx, u.ChatID)
		}

	case model.UpdateUserAdded:
		// Новый подписчик канала: если он уже запускал бота — сразу отправляем подборку.
		b.onUserJoined(ctx, u.ChatID, u.GetUser())
	}
}

// isDialog сообщает, что сообщение пришло из личного диалога с ботом.
func isDialog(r model.Recipient) bool {
	return r.ChatType == model.ChatTypeDialog || (r.ChatType == "" && r.UserID != 0)
}

// onStart — старт диалога или любое сообщение пользователя: записываем пользователя в файл,
// проверяем подписку и отвечаем.
func (b *Bot) onStart(ctx context.Context, user model.User) {
	if user.UserID == 0 {
		return
	}

	added, err := b.store.Add(user)
	switch {
	case err != nil:
		log.Printf("[error] не удалось записать пользователя %d в %s: %v", user.UserID, b.store.Path(), err)
	case added:
		log.Printf("новый пользователь: id=%d %s (%s), всего в файле: %d",
			user.UserID, displayName(user), formatUsername(user.Username), b.store.Count())
	}

	subscribed, err := b.isSubscribed(ctx, user.UserID)
	switch {
	case err != nil:
		log.Printf("[error] проверка подписки пользователя %d: %v", user.UserID, err)
		b.send(ctx, user.UserID, maxbot.NewMessage().SetText(textCheckFailed))
	case subscribed:
		b.send(ctx, user.UserID, b.catalogMessage(textSubscribed))
	default:
		b.send(ctx, user.UserID, b.subscribeMessage(user))
	}
}

// onCheckCallback — нажатие кнопки «Я подписался».
func (b *Bot) onCheckCallback(ctx context.Context, cb model.Callback) {
	user := cb.User
	if user.UserID == 0 {
		return
	}

	subscribed, err := b.isSubscribed(ctx, user.UserID)
	switch {
	case err != nil:
		log.Printf("[error] проверка подписки пользователя %d: %v", user.UserID, err)
		b.answerNotification(ctx, cb.CallbackID, textCheckFailed)
	case subscribed:
		// Заменяем сообщение с просьбой подписаться на сообщение с подборкой.
		body := b.catalogMessage(textSubscribed).MessageBody()
		_, err := b.api.Messages.AnswerOnCallback(ctx, cb.CallbackID, model.CallbackAnswer{Message: &body})
		if err != nil {
			log.Printf("[warn] ответ на callback пользователя %d: %v — отправляю отдельным сообщением", user.UserID, err)
			b.send(ctx, user.UserID, b.catalogMessage(textSubscribed))
		}
		log.Printf("пользователь %d подтвердил подписку, подборка выдана", user.UserID)
	default:
		b.answerNotification(ctx, cb.CallbackID, textNotSubscribedYet)
	}
}

// onAddedToChannel — бота добавили в канал: выводим идентификатор канала в лог и,
// если это наш канал, запоминаем его.
func (b *Bot) onAddedToChannel(ctx context.Context, chatID int64) {
	chat, err := b.api.Chats.GetChat(ctx, chatID)
	if err != nil {
		log.Printf("бот добавлен в канал chat_id=%d (не удалось получить информацию о канале: %v)", chatID, err)

		return
	}

	log.Printf("бот добавлен в канал «%s» (chat_id=%d, link=%s)", chat.Title, chat.ChatID, chat.Link)

	if b.ChannelID() == 0 && b.isTargetChannel(chat) {
		b.setChannel(chat)
	}
}

// onUserJoined — в канал вступил пользователь.
func (b *Bot) onUserJoined(ctx context.Context, chatID int64, user model.User) {
	if user.UserID == 0 || user.IsBot {
		return
	}

	channelID := b.ChannelID()
	if channelID == 0 || chatID != channelID {
		return
	}

	if !b.store.Has(user.UserID) {
		return
	}

	log.Printf("пользователь %d подписался на канал — отправляю подборку", user.UserID)
	b.send(ctx, user.UserID, b.catalogMessage(textJustJoined))
}

// isSubscribed проверяет, является ли пользователь участником канала.
// Для этого бот должен быть администратором канала.
func (b *Bot) isSubscribed(ctx context.Context, userID int64) (bool, error) {
	channelID, err := b.resolveChannel(ctx)
	if err != nil {
		return false, err
	}

	list, err := b.api.Chats.GetMembers(ctx, channelID, 0, 0, []int64{userID})
	if err != nil {
		return false, fmt.Errorf("получение участников канала %d: %w (бот должен быть администратором канала)", channelID, err)
	}

	for _, m := range list.Members {
		if m.UserID == userID {
			return true, nil
		}
	}

	return false, nil
}

// ChannelID — идентификатор канала, если он уже известен.
func (b *Bot) ChannelID() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.channelID
}

// resolveChannel возвращает идентификатор канала, при необходимости определяя его
// по публичному имени через API.
func (b *Bot) resolveChannel(ctx context.Context) (int64, error) {
	if id := b.ChannelID(); id != 0 {
		return id, nil
	}

	chat, err := b.api.Chats.GetChatByLink(ctx, b.cfg.ChannelMention())
	if err != nil {
		return 0, fmt.Errorf("не удалось определить канал %s: %w", b.cfg.ChannelMention(), err)
	}
	if chat.Type == model.ChatTypeDialog || chat.ChatID == 0 {
		return 0, fmt.Errorf("%s — это не канал и не чат (type=%q)", b.cfg.ChannelMention(), chat.Type)
	}

	b.setChannel(chat)

	return chat.ChatID, nil
}

func (b *Bot) setChannel(chat model.Chat) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.channelID = chat.ChatID
	b.channelTitle = chat.Title
	if chat.Link != "" {
		b.channelLink = chat.Link
	}

	log.Printf("канал для проверки подписки: «%s» (chat_id=%d, link=%s)", chat.Title, chat.ChatID, b.channelLink)
}

// isTargetChannel проверяет, что ссылка канала соответствует настроенному имени.
func (b *Bot) isTargetChannel(chat model.Chat) bool {
	name, err := normalizeChannel(chat.Link)
	if err != nil {
		return false
	}

	return strings.EqualFold(name, b.cfg.ChannelUsername)
}

func (b *Bot) channelLinkURL() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.channelLink
}

// subscribeMessage — сообщение с просьбой подписаться и кнопками «Подписаться» / «Я подписался».
func (b *Bot) subscribeMessage(user model.User) *maxbot.Message {
	name := displayName(user)
	if name == "" {
		name = "друг"
	}

	keyboard := model.NewKeyboard()
	keyboard.AddRow().AddLink(buttonSubscribe, b.channelLinkURL())
	keyboard.AddRow().AddCallBack(buttonCheck, callbackCheckSubscription)

	return maxbot.NewMessage().
		SetText(fmt.Sprintf(textNeedSubscribe, name, b.cfg.ChannelMention())).
		AddKeyboard(keyboard)
}

// catalogMessage — сообщение со ссылкой на подборку товаров.
func (b *Bot) catalogMessage(text string) *maxbot.Message {
	keyboard := model.NewKeyboard()
	keyboard.AddRow().AddLink(buttonCatalog, b.cfg.CatalogURL)

	return maxbot.NewMessage().
		SetText(fmt.Sprintf(text, b.cfg.ChannelMention())).
		AddKeyboard(keyboard)
}

// send отправляет сообщение пользователю в личный диалог.
// В Max личные сообщения адресуются по user_id, а не по chat_id.
func (b *Bot) send(ctx context.Context, userID int64, msg *maxbot.Message) {
	if _, err := b.api.Messages.Send(ctx, msg.SetUser(userID)); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Printf("[error] отправка сообщения пользователю %d: %v", userID, err)
	}
}

// answerNotification показывает пользователю всплывающее уведомление в ответ на нажатие кнопки.
func (b *Bot) answerNotification(ctx context.Context, callbackID, text string) {
	answer := model.CallbackAnswer{Notification: &text}
	if _, err := b.api.Messages.AnswerOnCallback(ctx, callbackID, answer); err != nil {
		log.Printf("[error] ответ на callback %s: %v", callbackID, err)
	}
}
