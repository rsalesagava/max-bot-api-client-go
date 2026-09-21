package main

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
)

const (
	defaultChannel    = "@megastroy_diy"
	defaultCatalogURL = "https://megastroy.com/catalog/groups?products_groups%5B%5D=22"
	defaultUsersFile  = "users.txt"
	defaultMaxHost    = "https://max.ru/"
)

// Config — настройки бота. Все параметры задаются через переменные окружения.
type Config struct {
	// Token — токен бота (BOT_TOKEN). Обязателен.
	Token string
	// ChannelUsername — публичное имя канала без «@» (CHANNEL, по умолчанию megastroy_diy).
	ChannelUsername string
	// ChannelID — числовой идентификатор канала (CHANNEL_ID). Необязателен: если не задан,
	// бот попытается определить его сам по публичному имени канала.
	ChannelID int64
	// CatalogURL — ссылка на подборку товаров, которую получает подписчик (CATALOG_URL).
	CatalogURL string
	// UsersFile — путь к текстовому файлу со списком пользователей, запустивших бота (USERS_FILE).
	UsersFile string
	// CACertFile — путь к PEM-файлу с дополнительным корневым сертификатом (CA_CERT_FILE),
	// например сертификатом Минцифры, если его нет в системном хранилище.
	CACertFile string
	// APIBaseURL — адрес Bot API (MAX_API_URL). Нужен только для тестов и отладки;
	// по умолчанию используется боевой адрес из библиотеки.
	APIBaseURL string
}

// ChannelLink — публичная ссылка на канал, если API не вернул свою.
func (c Config) ChannelLink() string {
	return defaultMaxHost + c.ChannelUsername
}

// ChannelMention — имя канала в виде «@username» для текста сообщений.
func (c Config) ChannelMention() string {
	return "@" + c.ChannelUsername
}

func loadConfig() (Config, error) {
	cfg := Config{
		Token:      strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		CatalogURL: envOrDefault("CATALOG_URL", defaultCatalogURL),
		UsersFile:  envOrDefault("USERS_FILE", defaultUsersFile),
		CACertFile: strings.TrimSpace(os.Getenv("CA_CERT_FILE")),
		APIBaseURL: strings.TrimSpace(os.Getenv("MAX_API_URL")),
	}

	if cfg.Token == "" {
		return cfg, fmt.Errorf("не задана переменная окружения BOT_TOKEN (токен бота из MAX для бизнеса)")
	}

	username, err := normalizeChannel(envOrDefault("CHANNEL", defaultChannel))
	if err != nil {
		return cfg, fmt.Errorf("некорректное значение CHANNEL: %w", err)
	}
	cfg.ChannelUsername = username

	if raw := strings.TrimSpace(os.Getenv("CHANNEL_ID")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id == 0 {
			return cfg, fmt.Errorf("некорректное значение CHANNEL_ID=%q: ожидается числовой идентификатор канала", raw)
		}
		cfg.ChannelID = id
	}

	if _, err := url.ParseRequestURI(cfg.CatalogURL); err != nil {
		return cfg, fmt.Errorf("некорректное значение CATALOG_URL: %w", err)
	}

	if cfg.APIBaseURL != "" {
		if _, err := url.ParseRequestURI(cfg.APIBaseURL); err != nil {
			return cfg, fmt.Errorf("некорректное значение MAX_API_URL: %w", err)
		}
	}

	return cfg, nil
}

// normalizeChannel извлекает публичное имя канала из «@name», «name» или «https://max.ru/name».
func normalizeChannel(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("пустое имя канала")
	}

	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		raw = path.Base(strings.TrimSuffix(u.Path, "/"))
	}

	name := strings.TrimPrefix(raw, "@")
	if name == "" || name == "." || strings.ContainsAny(name, "/?#@ \t") {
		return "", fmt.Errorf("не удалось извлечь имя канала из %q", raw)
	}

	return name, nil
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}

	return def
}
