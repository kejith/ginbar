package auth

import (
	"errors"
	"strings"
	"testing"
)

func testPasswordParams() PasswordParams {
	return PasswordParams{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltBytes:   16,
		KeyBytes:    32,
	}
}

func TestPasswordVerifierRoundTripAndParameters(t *testing.T) {
	params := testPasswordParams()
	verifier, err := HashPassword("correct horse battery staple", params)
	if err != nil {
		t.Fatal(err)
	}
	if verifier == "correct horse battery staple" || !strings.HasPrefix(verifier, "$argon2id$") {
		t.Fatalf("unexpected verifier format: %q", verifier)
	}

	ok, err := VerifyPassword("correct horse battery staple", verifier)
	if err != nil || !ok {
		t.Fatalf("verify valid password: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("incorrect horse battery staple", verifier)
	if err != nil {
		t.Fatalf("verify incorrect password: %v", err)
	}
	if ok {
		t.Fatal("incorrect password unexpectedly verified")
	}

	parsed, salt, key, err := parseVerifier(verifier)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != params || len(salt) != int(params.SaltBytes) || len(key) != int(params.KeyBytes) {
		t.Fatalf("parsed params=%#v salt=%d key=%d", parsed, len(salt), len(key))
	}
}

func TestPasswordHashUsesRandomSalt(t *testing.T) {
	first, err := HashPassword("correct horse battery staple", testPasswordParams())
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword("correct horse battery staple", testPasswordParams())
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two password hashes reused the same salt")
	}
}

func TestMalformedPasswordVerifierFailsSafely(t *testing.T) {
	for _, verifier := range []string{
		"",
		"not-a-verifier",
		"$argon2i$v=19$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=18$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=999999999,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=8192,t=1,p=1$%%%$%%%",
		strings.Repeat("A", maxVerifierEncodedBytes+1),
	} {
		ok, err := VerifyPassword("correct horse battery staple", verifier)
		if ok || !errors.Is(err, ErrMalformedVerifier) {
			t.Fatalf("verifier length=%d: ok=%v err=%v", len(verifier), ok, err)
		}
	}
}

func TestPasswordInputBounds(t *testing.T) {
	params := testPasswordParams()
	if _, err := HashPassword(strings.Repeat("a", MinPasswordBytes-1), params); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("short password error = %v", err)
	}
	if _, err := HashPassword(strings.Repeat("a", MaxPasswordBytes+1), params); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("long password error = %v", err)
	}
	if _, err := HashPassword(strings.Repeat("a", MinPasswordBytes), params); err != nil {
		t.Fatalf("minimum password rejected: %v", err)
	}
}

func BenchmarkPasswordHash(b *testing.B) {
	params := DefaultPasswordParams()
	for i := 0; i < b.N; i++ {
		if _, err := HashPassword("benchmark-password-for-ginbar", params); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPasswordVerify(b *testing.B) {
	params := DefaultPasswordParams()
	verifier, err := HashPassword("benchmark-password-for-ginbar", params)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ok, err := VerifyPassword("benchmark-password-for-ginbar", verifier)
		if err != nil || !ok {
			b.Fatalf("ok=%v err=%v", ok, err)
		}
	}
}

func BenchmarkPasswordVerifyParallel(b *testing.B) {
	params := DefaultPasswordParams()
	verifier, err := HashPassword("benchmark-password-for-ginbar", params)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ok, err := VerifyPassword("benchmark-password-for-ginbar", verifier)
			if err != nil || !ok {
				b.Fatalf("ok=%v err=%v", ok, err)
			}
		}
	})
}

func BenchmarkPasswordParameterCandidates(b *testing.B) {
	candidates := []struct {
		name   string
		params PasswordParams
	}{
		{name: "32MiB-t2", params: PasswordParams{MemoryKiB: 32 * 1024, Iterations: 2, Parallelism: 1, SaltBytes: 16, KeyBytes: 32}},
		{name: "64MiB-t1", params: PasswordParams{MemoryKiB: 64 * 1024, Iterations: 1, Parallelism: 1, SaltBytes: 16, KeyBytes: 32}},
		{name: "64MiB-t2", params: PasswordParams{MemoryKiB: 64 * 1024, Iterations: 2, Parallelism: 1, SaltBytes: 16, KeyBytes: 32}},
		{name: "128MiB-t1", params: PasswordParams{MemoryKiB: 128 * 1024, Iterations: 1, Parallelism: 1, SaltBytes: 16, KeyBytes: 32}},
	}
	for _, candidate := range candidates {
		candidate := candidate
		b.Run(candidate.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := HashPassword("benchmark-password-for-ginbar", candidate.params); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
