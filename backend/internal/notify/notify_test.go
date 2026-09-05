package notify_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"tms/backend/internal/config"
	"tms/backend/internal/notify"
)

func TestLogProviderLogsRecipientAndBody(t *testing.T) {
	var buf bytes.Buffer
	p := notify.NewLogProvider(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	id, err := p.Send(context.Background(), "+255700000001", "Your TMS code is 123456")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if id == "" {
		t.Fatal("Send() returned empty provider message id")
	}

	out := buf.String()
	for _, want := range []string{"sms_to=+255700000001", "sms_body=", "123456", "level=INFO"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q\ngot: %s", want, out)
		}
	}
}

func TestBeemProviderUnconfiguredErrors(t *testing.T) {
	p := notify.NewBeemProvider(config.Config{})

	if p.Configured() {
		t.Fatal("Configured() = true with empty credentials")
	}
	if _, err := p.Send(context.Background(), "+255700000001", "hi"); !errors.Is(err, notify.ErrBeemNotConfigured) {
		t.Fatalf("Send() error = %v, want ErrBeemNotConfigured", err)
	}
	if got := notify.ErrBeemNotConfigured.Error(); got != "beem: not configured" {
		t.Fatalf("error text = %q", got)
	}
}

func TestProviderForSelection(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	if _, ok := notify.ProviderFor(config.Config{}, logger).(*notify.LogProvider); !ok {
		t.Error("empty BEEM_API_KEY should select LogProvider")
	}
	cfg := config.Config{BeemAPIKey: "key", BeemSecretKey: "secret"}
	if _, ok := notify.ProviderFor(cfg, logger).(*notify.BeemProvider); !ok {
		t.Error("set BEEM_API_KEY should select BeemProvider")
	}
}
