package random

import (
	"encoding/base64"
	"strings"
	"sync"
	"testing"
)

func TestSeqLength(t *testing.T) {
	for _, n := range []int{1, 8, 20, 32, 64, 256} {
		got, err := Seq(n)
		if err != nil {
			t.Fatalf("Seq(%d): %v", n, err)
		}
		if len(got) != n {
			t.Errorf("Seq(%d) returned %d chars", n, len(got))
		}
	}
}

func TestSeqNonPositive(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		got, err := Seq(n)
		if err != nil {
			t.Errorf("Seq(%d) must not error, got %v", n, err)
		}
		if got != "" {
			t.Errorf("Seq(%d) = %q, want empty", n, got)
		}
	}
}

func TestSeqUsesOnlyTheAlphabet(t *testing.T) {
	got, err := Seq(4096)
	if err != nil {
		t.Fatalf("Seq: %v", err)
	}
	for i, r := range got {
		if !strings.ContainsRune(alphabet, r) {
			t.Fatalf("Seq produced %q at index %d, which is outside the alphabet", r, i)
		}
	}
}

// TestSeqIsNotPredictable 是 C-1 的行为层面回归。
//
// 旧实现是 math/rand + time.Now().UnixNano() 播种：同一毫秒内启动的两个进程
// 会生成完全相同的串，攻击者按启动时间窗口就能穷举出 session secret。
// 这里不做统计学检验，只要求"独立调用不会撞车"——弱 RNG 在这条上就会挂。
func TestSeqIsNotPredictable(t *testing.T) {
	const samples = 512
	seen := make(map[string]bool, samples)
	for i := 0; i < samples; i++ {
		got, err := Seq(16)
		if err != nil {
			t.Fatalf("Seq: %v", err)
		}
		if seen[got] {
			t.Fatalf("Seq returned the duplicate %q after %d draws", got, i)
		}
		seen[got] = true
	}
}

// TestSeqCoversTheAlphabet 粗略验证取样无偏：足够多的样本应覆盖到每个字符。
// 取模偏置或退化的 RNG 会让某些字符永远取不到。
func TestSeqCoversTheAlphabet(t *testing.T) {
	got, err := Seq(len(alphabet) * 200)
	if err != nil {
		t.Fatalf("Seq: %v", err)
	}
	for _, r := range alphabet {
		if !strings.ContainsRune(got, r) {
			t.Errorf("character %q never appeared in %d draws", r, len(got))
		}
	}
}

func TestSeqIsConcurrencySafe(t *testing.T) {
	const goroutines = 32
	results := make([]string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			s, err := Seq(32)
			if err != nil {
				t.Errorf("Seq: %v", err)
				return
			}
			results[i] = s
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, goroutines)
	for i, s := range results {
		if len(s) != 32 {
			t.Errorf("goroutine %d produced %d chars", i, len(s))
		}
		if seen[s] {
			t.Errorf("goroutine %d produced a duplicate value", i)
		}
		seen[s] = true
	}
}

func TestMustSeq(t *testing.T) {
	if got := MustSeq(24); len(got) != 24 {
		t.Errorf("MustSeq(24) returned %d chars", len(got))
	}
	if got := MustSeq(0); got != "" {
		t.Errorf("MustSeq(0) = %q, want empty", got)
	}
}

func TestBytes(t *testing.T) {
	for _, n := range []int{0, 1, 32, 64} {
		got, err := Bytes(n)
		if err != nil {
			t.Fatalf("Bytes(%d): %v", n, err)
		}
		if len(got) != n {
			t.Errorf("Bytes(%d) returned %d bytes", n, len(got))
		}
	}
}

func TestBytesAreNotAllZero(t *testing.T) {
	got, err := Bytes(64)
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	allZero := true
	for _, b := range got {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("Bytes returned 64 zero bytes; the entropy source is not working")
	}
}

// TestSecretString 是 session cookie 密钥的形态约定：
// base64(RawURL) 可以安全地存进 settings 表的 TEXT 列。
func TestSecretString(t *testing.T) {
	got, err := SecretString(32)
	if err != nil {
		t.Fatalf("SecretString: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("SecretString produced %q, which is not raw-url base64: %v", got, err)
	}
	if len(raw) != 32 {
		t.Errorf("SecretString(32) decodes to %d bytes", len(raw))
	}
	if strings.ContainsAny(got, "+/=") {
		t.Errorf("SecretString produced %q, which needs escaping", got)
	}
}

func TestSecretStringIsUnique(t *testing.T) {
	seen := make(map[string]bool, 128)
	for i := 0; i < 128; i++ {
		got, err := SecretString(32)
		if err != nil {
			t.Fatalf("SecretString: %v", err)
		}
		if seen[got] {
			t.Fatalf("SecretString returned the duplicate %q", got)
		}
		seen[got] = true
	}
}
