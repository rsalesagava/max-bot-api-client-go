package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/max-messenger/max-bot-api-client-go/v2/model"
)

const (
	storeTimeLayout = "2006-01-02 15:04:05"
	storeSeparator  = "\t"
	storeEmpty      = "-"
)

// UserStore — хранилище пользователей, запустивших бота, в обычном текстовом файле (без БД).
//
// Каждый пользователь записывается один раз, строкой вида:
//
//	2026-09-21 14:05:00<TAB>123456789<TAB>@username<TAB>Иван Петров
//
// Идентификаторы уже записанных пользователей держатся в памяти, чтобы не дублировать строки.
type UserStore struct {
	mu   sync.Mutex
	path string
	seen map[int64]struct{}
}

// OpenUserStore открывает (или создаёт) файл и загружает из него идентификаторы пользователей.
func OpenUserStore(path string) (*UserStore, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create dir for users file: %w", err)
		}
	}

	s := &UserStore{
		path: path,
		seen: make(map[int64]struct{}),
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

// Path — путь к файлу.
func (s *UserStore) Path() string {
	return s.path
}

// Count — количество уникальных пользователей в файле.
func (s *UserStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.seen)
}

// Has сообщает, записан ли пользователь.
func (s *UserStore) Has(userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.seen[userID]

	return ok
}

// Add дописывает пользователя в файл, если его там ещё нет.
// Возвращает true, если пользователь был добавлен.
func (s *UserStore) Add(user model.User) (bool, error) {
	if user.UserID == 0 {
		return false, fmt.Errorf("empty user id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.seen[user.UserID]; ok {
		return false, nil
	}

	line := strings.Join([]string{
		time.Now().Format(storeTimeLayout),
		strconv.FormatInt(user.UserID, 10),
		formatUsername(user.Username),
		orEmpty(sanitize(displayName(user))),
	}, storeSeparator) + "\n"

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("open users file: %w", err)
	}

	_, writeErr := f.WriteString(line)
	closeErr := f.Close()
	if writeErr != nil {
		return false, fmt.Errorf("write users file: %w", writeErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close users file: %w", closeErr)
	}

	s.seen[user.UserID] = struct{}{}

	return true, nil
}

func (s *UserStore) load() error {
	f, err := os.OpenFile(s.path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("open users file: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), storeSeparator)
		if len(fields) < 2 {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
		if err != nil || id == 0 {
			continue
		}
		s.seen[id] = struct{}{}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, fs.ErrClosed) {
		return fmt.Errorf("read users file: %w", err)
	}

	return nil
}

// displayName — отображаемое имя пользователя: «Имя Фамилия», либо устаревшее поле name.
func displayName(u model.User) string {
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	if name == "" {
		name = strings.TrimSpace(u.Name)
	}

	return name
}

func formatUsername(username string) string {
	username = sanitize(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if username == "" {
		return storeEmpty
	}

	return "@" + username
}

// sanitize убирает из значения символы, ломающие построчный формат файла.
func sanitize(v string) string {
	v = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(v)

	return strings.TrimSpace(v)
}

func orEmpty(v string) string {
	if v == "" {
		return storeEmpty
	}

	return v
}
