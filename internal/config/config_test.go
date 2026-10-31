package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL != "sqlite:///var/lib/eva-manager/eva.db" {
		t.Errorf("DatabaseURL = %q", c.DatabaseURL)
	}
	if c.Listen != "127.0.0.1:8080" || c.XrayAPI != "127.0.0.1:10085" {
		t.Errorf("Listen = %q, XrayAPI = %q", c.Listen, c.XrayAPI)
	}
}

func TestDataDirMovesSQLite(t *testing.T) {
	c, err := load(env(map[string]string{EnvDataDir: "/srv/eva"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL != "sqlite:///srv/eva/eva.db" {
		t.Errorf("DatabaseURL = %q", c.DatabaseURL)
	}
}

func TestXrayAPIMustBeLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:10085", "10.0.0.1:10085", "example.com:10085"} {
		_, err := load(env(map[string]string{EnvXrayAPI: addr}))
		if err == nil || !strings.Contains(err.Error(), EnvXrayAPI) {
			t.Errorf("%s: err = %v, ожидался отказ", addr, err)
		}
	}
	if _, err := load(env(map[string]string{EnvXrayAPI: "[::1]:10085"})); err != nil {
		t.Errorf("::1: %v", err)
	}
}

func TestBadListen(t *testing.T) {
	if _, err := load(env(map[string]string{EnvListen: "8080"})); err == nil {
		t.Error("адрес без хоста принят")
	}
}
