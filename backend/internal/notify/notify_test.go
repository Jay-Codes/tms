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

func TestSMSProviderForSelection(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	p, err := notify.SMSProviderFor(config.Config{}, logger)
	if err != nil {
		t.Fatalf("dev selection error = %v", err)
	}
	if _, ok := p.(*notify.LogProvider); !ok {
		t.Errorf("empty BEEM_API_KEY in dev should select LogProvider, got %T", p)
	}

	cfg := config.Config{BeemAPIKey: "key", BeemSecretKey: "secret"}
	p, err = notify.SMSProviderFor(cfg, logger)
	if err != nil {
		t.Fatalf("beem selection error = %v", err)
	}
	if _, ok := p.(*notify.BeemProvider); !ok {
		t.Errorf("set BEEM_API_KEY should select BeemProvider, got %T", p)
	}

	// prod + Beem configured is fine.
	prodBeem := config.Config{Env: config.EnvProd, BeemAPIKey: "key", BeemSecretKey: "secret"}
	if _, err := notify.SMSProviderFor(prodBeem, logger); err != nil {
		t.Fatalf("prod with Beem configured error = %v", err)
	}
}

// TestProvidersRefusedInProd is the M4 guard: ENV=prod must never fall back to
// the dev log providers, which write OTP codes and invite links to the log.
func TestProvidersRefusedInProd(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	prod := config.Config{Env: config.EnvProd}

	p, err := notify.SMSProviderFor(prod, logger)
	if !errors.Is(err, notify.ErrDevProviderInProd) {
		t.Errorf("SMSProviderFor(prod) error = %v, want ErrDevProviderInProd", err)
	}
	if p != nil {
		t.Errorf("SMSProviderFor(prod) provider = %T, want nil", p)
	}

	ep, err := notify.EmailProviderFor(prod, logger)
	if !errors.Is(err, notify.ErrDevProviderInProd) {
		t.Errorf("EmailProviderFor(prod) error = %v, want ErrDevProviderInProd", err)
	}
	if ep != nil {
		t.Errorf("EmailProviderFor(prod) provider = %T, want nil", ep)
	}

	if _, err := notify.EmailProviderFor(config.Config{}, logger); err != nil {
		t.Errorf("EmailProviderFor(dev) error = %v", err)
	}
}

func TestDisabledProvidersError(t *testing.T) {
	if _, err := (notify.DisabledSMSProvider{}).Send(context.Background(), "+255700000001", "hi"); !errors.Is(err, notify.ErrProviderDisabled) {
		t.Errorf("DisabledSMSProvider.Send error = %v", err)
	}
	if _, err := (notify.DisabledEmailProvider{}).Send(context.Background(), "a@b.test", "s", "l", "b"); !errors.Is(err, notify.ErrProviderDisabled) {
		t.Errorf("DisabledEmailProvider.Send error = %v", err)
	}
}
