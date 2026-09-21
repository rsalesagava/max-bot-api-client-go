// Бот для мессенджера Max: проверяет подписку пользователя на канал
// и после подписки выдаёт ссылку на подборку товаров.
//
// Сценарий:
//  1. Пользователь запускает бота (или пишет ему любое сообщение) — бот записывает
//     его в текстовый файл и проверяет, подписан ли он на канал (по умолчанию @megastroy_diy).
//  2. Если подписан — бот сразу присылает кнопку со ссылкой на подборку товаров.
//  3. Если нет — бот просит подписаться и даёт кнопки «Подписаться на канал» и «Я подписался».
//     По нажатию «Я подписался» подписка проверяется повторно.
//  4. Если пользователь подписался на канал после запуска бота, бот присылает подборку сам.
//
// База данных не используется: пользователи, запустившие бота, дописываются в текстовый файл.
//
// Переменные окружения:
//
//	BOT_TOKEN    — токен бота (обязательно)
//	CHANNEL      — канал для проверки подписки, по умолчанию @megastroy_diy
//	CHANNEL_ID   — числовой идентификатор канала (необязательно; если не задан, определяется по CHANNEL)
//	CATALOG_URL  — ссылка на подборку товаров
//	USERS_FILE   — путь к файлу со списком пользователей, по умолчанию users.txt
//	CA_CERT_FILE — PEM-файл с дополнительным корневым сертификатом (например, Минцифры)
//	MAX_API_URL  — адрес Bot API (только для тестов и отладки)
//
// Важно: бот должен быть администратором канала — иначе Max не даст проверить подписку.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go/v2"
)

const (
	// pollTimeout — таймаут long polling на стороне Max.
	pollTimeout = 30 * time.Second
	// httpTimeout — таймаут HTTP-запроса; должен быть больше pollTimeout.
	httpTimeout = pollTimeout + 15*time.Second
	// retryPause — пауза перед повтором после ошибки получения обновлений.
	retryPause = 5 * time.Second
)

func main() {
	log.SetFlags(log.LstdFlags)

	if err := run(); err != nil {
		log.Fatalf("[fatal] %v", err)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpClient, err := newHTTPClient(cfg.CACertFile)
	if err != nil {
		return err
	}

	opts := []maxbot.Opt{
		maxbot.WithHTTPClient(httpClient),
		maxbot.WithPollingTimeout(pollTimeout),
	}
	if cfg.APIBaseURL != "" {
		opts = append(opts, maxbot.WithBaseURL(cfg.APIBaseURL))
	}

	api, err := maxbot.NewApi(cfg.Token, opts...)
	if err != nil {
		return fmt.Errorf("инициализация API: %w", err)
	}

	info, err := api.Bots.GetMyInfo(ctx)
	if err != nil {
		return fmt.Errorf("не удалось получить информацию о боте (проверьте BOT_TOKEN): %w", err)
	}
	log.Printf("бот запущен: @%s (id=%d)", info.Username, info.UserID)

	store, err := OpenUserStore(cfg.UsersFile)
	if err != nil {
		return fmt.Errorf("файл пользователей: %w", err)
	}
	log.Printf("файл пользователей: %s (записей: %d)", store.Path(), store.Count())
	log.Printf("канал: %s, подборка: %s", cfg.ChannelMention(), cfg.CatalogURL)

	bot := NewBot(api, store, cfg)
	bot.Init(ctx)

	pollUpdates(ctx, api, bot)
	log.Println("бот остановлен")

	return nil
}

// pollUpdates получает события через long polling и передаёт их боту, пока не отменён контекст.
func pollUpdates(ctx context.Context, api *maxbot.Api, bot *Bot) {
	var marker int64

	for ctx.Err() == nil {
		updates, next, err := api.Subscriptions.GetUpdates(ctx, marker)
		if ctx.Err() != nil {
			return
		}

		var timeoutErr *maxbot.TimeoutError
		if errors.As(err, &timeoutErr) {
			// Обычное завершение long polling без новых событий.
			continue
		}
		if err != nil {
			log.Printf("[error] получение обновлений: %v (повтор через %s)", err, retryPause)
			select {
			case <-ctx.Done():
			case <-time.After(retryPause):
			}

			continue
		}

		marker = next
		for _, u := range updates {
			if ctx.Err() != nil {
				return
			}
			bot.Handle(ctx, u)
		}
	}
}

// newHTTPClient создаёт HTTP-клиент. Если задан caCertFile, сертификат из него добавляется
// к системным доверенным сертификатам (Max использует TLS-сертификаты, выпущенные
// удостоверяющим центром Минцифры, которых может не быть в системном хранилище).
func newHTTPClient(caCertFile string) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("unexpected default transport type")
	}
	transport = transport.Clone()

	if caCertFile != "" {
		pem, err := os.ReadFile(caCertFile)
		if err != nil {
			return nil, fmt.Errorf("чтение CA_CERT_FILE: %w", err)
		}

		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA_CERT_FILE=%s не содержит сертификатов в формате PEM", caCertFile)
		}

		transport.TLSClientConfig = &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}
	}

	return &http.Client{
		Timeout:   httpTimeout,
		Transport: transport,
	}, nil
}
