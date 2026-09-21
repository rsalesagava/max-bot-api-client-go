package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/max-messenger/max-bot-api-client-go/v2/model"
)

func TestUserStoreAddAndDedupe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.txt")

	store, err := OpenUserStore(path)
	if err != nil {
		t.Fatalf("OpenUserStore: %v", err)
	}

	ivan := model.User{UserID: 1, FirstName: "Иван", LastName: "Петров", Username: "ivan"}
	anna := model.User{UserID: 2, Name: "Anna"}

	added, err := store.Add(ivan)
	if err != nil || !added {
		t.Fatalf("first Add(ivan) = (%v, %v), want (true, nil)", added, err)
	}

	added, err = store.Add(ivan)
	if err != nil || added {
		t.Fatalf("second Add(ivan) = (%v, %v), want (false, nil)", added, err)
	}

	added, err = store.Add(anna)
	if err != nil || !added {
		t.Fatalf("Add(anna) = (%v, %v), want (true, nil)", added, err)
	}

	if _, err := store.Add(model.User{}); err == nil {
		t.Fatalf("Add(empty user) must fail")
	}

	if got := store.Count(); got != 2 {
		t.Fatalf("Count() = %d, want 2", got)
	}
	if !store.Has(1) || !store.Has(2) || store.Has(3) {
		t.Fatalf("Has() reports wrong membership")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("file has %d lines, want 2:\n%s", len(lines), data)
	}

	fields := strings.Split(lines[0], "\t")
	if len(fields) != 4 {
		t.Fatalf("line has %d fields, want 4: %q", len(fields), lines[0])
	}
	if fields[1] != "1" || fields[2] != "@ivan" || fields[3] != "Иван Петров" {
		t.Fatalf("unexpected line: %q", lines[0])
	}

	fields = strings.Split(lines[1], "\t")
	if fields[1] != "2" || fields[2] != "-" || fields[3] != "Anna" {
		t.Fatalf("unexpected line: %q", lines[1])
	}
}

func TestUserStoreReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "users.txt")

	store, err := OpenUserStore(path)
	if err != nil {
		t.Fatalf("OpenUserStore: %v", err)
	}
	for _, id := range []int64{10, 20, 30} {
		if _, err := store.Add(model.User{UserID: id, FirstName: "user"}); err != nil {
			t.Fatalf("Add(%d): %v", id, err)
		}
	}

	// Человек мог дописать в файл что-то руками — такие строки не должны ломать загрузку.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _ = f.WriteString("\n# комментарий\nмусор без табуляций\n2026-01-01 00:00:00\tне-число\t-\t-\n")
	_ = f.Close()

	reloaded, err := OpenUserStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reloaded.Count(); got != 3 {
		t.Fatalf("Count() after reload = %d, want 3", got)
	}
	for _, id := range []int64{10, 20, 30} {
		if !reloaded.Has(id) {
			t.Fatalf("user %d lost after reload", id)
		}
	}

	added, err := reloaded.Add(model.User{UserID: 20})
	if err != nil || added {
		t.Fatalf("Add(existing) after reload = (%v, %v), want (false, nil)", added, err)
	}
}

func TestUserStoreSanitize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.txt")

	store, err := OpenUserStore(path)
	if err != nil {
		t.Fatalf("OpenUserStore: %v", err)
	}

	user := model.User{UserID: 7, FirstName: "Пётр\tИ.", LastName: "Сидоров\nмладший", Username: "@petr\r"}
	if _, err := store.Add(user); err != nil {
		t.Fatalf("Add: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	content := strings.TrimSuffix(string(data), "\n")
	if strings.Count(content, "\n") != 0 {
		t.Fatalf("user with newlines must be written as a single line, got:\n%s", data)
	}
	fields := strings.Split(content, "\t")
	if len(fields) != 4 {
		t.Fatalf("line has %d fields, want 4: %q", len(fields), content)
	}
	if fields[2] != "@petr" || fields[3] != "Пётр И. Сидоров младший" {
		t.Fatalf("unexpected sanitized fields: %q", fields)
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct {
		user model.User
		want string
	}{
		{model.User{FirstName: "Иван", LastName: "Петров"}, "Иван Петров"},
		{model.User{FirstName: "Иван"}, "Иван"},
		{model.User{LastName: "Петров", Name: "Ignored"}, "Петров"},
		{model.User{Name: "Old Name"}, "Old Name"},
		{model.User{}, ""},
	}

	for _, tc := range cases {
		if got := displayName(tc.user); got != tc.want {
			t.Errorf("displayName(%+v) = %q, want %q", tc.user, got, tc.want)
		}
	}
}
