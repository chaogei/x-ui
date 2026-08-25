package service

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	const plain = "correct horse battery staple"
	hashed, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hashed == plain {
		t.Fatal("HashPassword returned the plaintext")
	}
	if !IsBcryptHash(hashed) {
		t.Fatalf("HashPassword produced %q, which IsBcryptHash rejects", hashed)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)); err != nil {
		t.Fatalf("hash does not verify against its own plaintext: %v", err)
	}

	ok, needUpgrade := VerifyPassword(hashed, plain)
	if !ok {
		t.Error("VerifyPassword rejected the correct password")
	}
	if needUpgrade {
		t.Error("a bcrypt hash should never be reported as needing an upgrade")
	}

	if ok, _ := VerifyPassword(hashed, plain+"x"); ok {
		t.Error("VerifyPassword accepted a wrong password")
	}
}

// TestHashPasswordSaltsEachCall 保证两次哈希同一口令产出不同结果，
// 否则说明 salt 没有生效，彩虹表攻击重新可行。
func TestHashPasswordSaltsEachCall(t *testing.T) {
	a, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two hashes of the same password are identical, salting is broken")
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("HashPassword must reject an empty password")
	}
}

// TestVerifyPasswordPlaintextUpgrade 覆盖 v1.0.0 之前的明文遗留数据路径。
func TestVerifyPasswordPlaintextUpgrade(t *testing.T) {
	ok, needUpgrade := VerifyPassword("legacy-plaintext", "legacy-plaintext")
	if !ok {
		t.Error("a matching plaintext password should verify")
	}
	if !needUpgrade {
		t.Error("a matching plaintext password must be flagged for upgrade to bcrypt")
	}

	ok, needUpgrade = VerifyPassword("legacy-plaintext", "wrong")
	if ok {
		t.Error("a non-matching plaintext password should not verify")
	}
	if needUpgrade {
		t.Error("a failed comparison must not trigger an upgrade")
	}
}

func TestIsBcryptHash(t *testing.T) {
	real, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		real:                                true,
		"$2a$10$" + strings.Repeat("a", 53): true,
		"$2b$10$" + strings.Repeat("a", 53): true,
		"$2y$10$" + strings.Repeat("a", 53): true,
		"admin":                             false,
		"":                                  false,
		"$2a$10$tooshort":                   false,
		// 长度对但前缀不是 bcrypt。
		"$1a$10$" + strings.Repeat("a", 53): false,
		strings.Repeat("a", 60):             false,
	}
	for input, want := range cases {
		if got := IsBcryptHash(input); got != want {
			t.Errorf("IsBcryptHash(%q) = %v, want %v", input, got, want)
		}
	}
}
