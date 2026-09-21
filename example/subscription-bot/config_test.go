package main

import "testing"

func TestNormalizeChannel(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "@megastroy_diy", want: "megastroy_diy"},
		{in: "megastroy_diy", want: "megastroy_diy"},
		{in: "  @megastroy_diy  ", want: "megastroy_diy"},
		{in: "https://max.ru/megastroy_diy", want: "megastroy_diy"},
		{in: "https://max.ru/megastroy_diy/", want: "megastroy_diy"},
		{in: "https://max.ru/@megastroy_diy", want: "megastroy_diy"},
		{in: "https://web.max.ru/megastroy_diy?utm=1", want: "megastroy_diy"},
		{in: "", wantErr: true},
		{in: "@", wantErr: true},
		{in: "https://max.ru/", wantErr: true},
		{in: "a/b", wantErr: true},
		{in: "a b", wantErr: true},
	}

	for _, tc := range cases {
		got, err := normalizeChannel(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizeChannel(%q) = %q, want error", tc.in, got)
			}

			continue
		}
		if err != nil {
			t.Errorf("normalizeChannel(%q): unexpected error: %v", tc.in, err)

			continue
		}
		if got != tc.want {
			t.Errorf("normalizeChannel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("BOT_TOKEN", "")
	if _, err := loadConfig(); err == nil {
		t.Fatalf("loadConfig without BOT_TOKEN must fail")
	}

	t.Setenv("BOT_TOKEN", " token ")
	t.Setenv("CHANNEL", "")
	t.Setenv("CHANNEL_ID", "")
	t.Setenv("CATALOG_URL", "")
	t.Setenv("USERS_FILE", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Token != "token" {
		t.Errorf("Token = %q", cfg.Token)
	}
	if cfg.ChannelUsername != "megastroy_diy" || cfg.ChannelMention() != "@megastroy_diy" {
		t.Errorf("channel = %q / %q", cfg.ChannelUsername, cfg.ChannelMention())
	}
	if cfg.ChannelLink() != "https://max.ru/megastroy_diy" {
		t.Errorf("ChannelLink() = %q", cfg.ChannelLink())
	}
	if cfg.CatalogURL != defaultCatalogURL {
		t.Errorf("CatalogURL = %q", cfg.CatalogURL)
	}
	if cfg.UsersFile != defaultUsersFile {
		t.Errorf("UsersFile = %q", cfg.UsersFile)
	}
	if cfg.ChannelID != 0 {
		t.Errorf("ChannelID = %d, want 0", cfg.ChannelID)
	}

	t.Setenv("CHANNEL", "https://max.ru/other_channel")
	t.Setenv("CHANNEL_ID", "-70000000000007")
	t.Setenv("CATALOG_URL", "https://example.com/catalog")
	t.Setenv("USERS_FILE", "/tmp/users.txt")

	cfg, err = loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.ChannelUsername != "other_channel" {
		t.Errorf("ChannelUsername = %q", cfg.ChannelUsername)
	}
	if cfg.ChannelID != -70000000000007 {
		t.Errorf("ChannelID = %d", cfg.ChannelID)
	}
	if cfg.CatalogURL != "https://example.com/catalog" || cfg.UsersFile != "/tmp/users.txt" {
		t.Errorf("CatalogURL = %q, UsersFile = %q", cfg.CatalogURL, cfg.UsersFile)
	}

	t.Setenv("CHANNEL_ID", "abc")
	if _, err := loadConfig(); err == nil {
		t.Fatalf("loadConfig with bad CHANNEL_ID must fail")
	}

	t.Setenv("CHANNEL_ID", "")
	t.Setenv("CATALOG_URL", "not a url")
	if _, err := loadConfig(); err == nil {
		t.Fatalf("loadConfig with bad CATALOG_URL must fail")
	}
}
