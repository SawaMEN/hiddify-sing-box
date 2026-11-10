package xuispeed

import (
	"context"
	"os"
	"testing"
)

func TestBrokerOptionalWithoutConfig(t *testing.T) {
	t.Setenv(Environment, "")
	current, err := New("masque:demo:user")
	if err != nil || current != nil {
		t.Fatalf("no broker must mean unlimited budget, got %v %v", current, err)
	}
	current.Close()
	if err := current.Wait(context.Background(), 0, 4096); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerOnlyRequiresCredentialsForLimitedIdentity(t *testing.T) {
	cfg := Config{Address: "127.0.0.1:8181", Users: map[string]string{"masque:demo:user": "client"}}
	if got, err := NewWithConfig(cfg, "masque:demo:other"); err != nil || got != nil {
		t.Fatalf("unlimited user unexpectedly changed: %v %v", got, err)
	}
	if got, err := NewWithConfig(cfg, "masque:demo:user"); err == nil || got != nil {
		t.Fatalf("missing password unexpectedly accepted: %v %v", got, err)
	}
	cfg.Password = "secret"
	cfg.Address = "192.0.2.1:8181"
	if _, err := NewWithConfig(cfg, "masque:demo:user"); err == nil {
		t.Fatal("broker must be loopback only")
	}
	cfg.Address = "127.0.0.1:8181"
	got, err := NewWithConfig(cfg, "masque:demo:user")
	if err != nil || got == nil {
		t.Fatalf("valid broker rejected: %v", err)
	}
	got.Close()
	if err := got.Wait(context.Background(), 0, 8); err == nil {
		t.Fatal("closed broker accepted additional budget")
	}
	if err := got.Wait(context.Background(), 2, 8); err == nil {
		t.Fatal("closed broker accepted invalid direction")
	}
}

func TestInvalidBrokerConfigurationFailsClosed(t *testing.T) {
	t.Setenv(Environment, t.TempDir()+"/missing")
	if _, err := New("masque:demo:user"); err == nil {
		t.Fatal("missing broker config silently ignored")
	}
	file := t.TempDir()+"/speed.json"
	if err := os.WriteFile(file, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(Environment, file)
	if _, err := New("masque:demo:user"); err == nil {
		t.Fatal("malformed broker config silently ignored")
	}
}
