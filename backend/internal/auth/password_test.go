package auth_test

import (
	"strings"
	"testing"

	"tms/backend/internal/auth"
)

func TestHashSecretRoundTrip(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashSecret("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashSecret: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash = %q, want an argon2id PHC string", hash)
	}
	if !auth.VerifySecret(hash, "correct horse battery staple") {
		t.Fatal("VerifySecret rejected the correct secret")
	}
	if auth.VerifySecret(hash, "wrong secret") {
		t.Fatal("VerifySecret accepted the wrong secret")
	}
}

func TestHashSecretIsSalted(t *testing.T) {
	t.Parallel()
	a, _ := auth.HashSecret("1234")
	b, _ := auth.HashSecret("1234")
	if a == b {
		t.Fatal("two hashes of the same secret are identical — the salt is not random")
	}
}

func TestVerifySecretRejectsMalformedHashes(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"", "not-a-hash", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$bad$c2FsdA$aGFzaA"} {
		if auth.VerifySecret(h, "1234") {
			t.Errorf("VerifySecret accepted malformed hash %q", h)
		}
	}
}

func TestNewTokenIsOpaqueAndHashed(t *testing.T) {
	t.Parallel()
	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if len(token) < 40 {
		t.Fatalf("token %q is shorter than 32 random bytes", token)
	}
	if len(hash) != 64 {
		t.Fatalf("hash %q is not a sha256 hex digest", hash)
	}
	if auth.HashToken(token) != hash {
		t.Fatal("HashToken disagrees with NewToken")
	}
	if strings.Contains(hash, token) {
		t.Fatal("the stored hash leaks the token")
	}
}

func TestGenerateOTPIsSixDigits(t *testing.T) {
	t.Parallel()
	for i := 0; i < 50; i++ {
		code := auth.GenerateOTP()
		if len(code) != 6 {
			t.Fatalf("GenerateOTP = %q, want 6 digits", code)
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("GenerateOTP = %q, want digits only", code)
			}
		}
	}
}
