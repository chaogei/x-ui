package locale

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// 三份词典之间没有任何编译期约束。少一个键，那种语言的用户看到的就是
// 裸露的 key（例如 "auth_totp_required" 直接印在按钮上）；多一个键则是
// 删文案时漏改的死条目。这条用例就是它们唯一的护栏。

const translationDir = "../translation"

func loadKeys(t *testing.T, path string) map[string]string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	values := map[string]string{}
	if _, err := toml.Decode(string(raw), &values); err != nil {
		t.Fatalf("%s is not valid TOML: %v", path, err)
	}
	return values
}

func translationFiles(t *testing.T) map[string]string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(translationDir, "translate.*.toml"))
	if err != nil {
		t.Fatalf("glob translations: %v", err)
	}
	if len(matches) != len(Supported) {
		t.Fatalf("found %d translation files but %d supported languages: %v",
			len(matches), len(Supported), matches)
	}
	out := make(map[string]string, len(matches))
	for _, path := range matches {
		out[filepath.Base(path)] = path
	}
	return out
}

func TestEveryLanguageDefinesTheSameKeys(t *testing.T) {
	files := translationFiles(t)

	// 以简体中文为基准：它是 i18n.NewBundle 的默认语言，也是新文案落地的第一站。
	const reference = "translate.zh_Hans.toml"
	base := loadKeys(t, files[reference])
	if len(base) == 0 {
		t.Fatalf("%s has no keys at all", reference)
	}

	for name, path := range files {
		if name == reference {
			continue
		}
		t.Run(name, func(t *testing.T) {
			other := loadKeys(t, path)

			var missing, extra []string
			for key := range base {
				if _, ok := other[key]; !ok {
					missing = append(missing, key)
				}
			}
			for key := range other {
				if _, ok := base[key]; !ok {
					extra = append(extra, key)
				}
			}
			sort.Strings(missing)
			sort.Strings(extra)

			if len(missing) > 0 {
				t.Errorf("keys defined in %s but missing here: %v — those users would see the raw key",
					reference, missing)
			}
			if len(extra) > 0 {
				t.Errorf("keys defined here but not in %s: %v — dead translations", reference, extra)
			}
		})
	}
}

// 空值等同于缺失：go-i18n 会把它渲染成空字符串，按钮上什么都不显示。
func TestNoTranslationIsEmpty(t *testing.T) {
	for name, path := range translationFiles(t) {
		t.Run(name, func(t *testing.T) {
			for key, value := range loadKeys(t, path) {
				if strings.TrimSpace(value) == "" {
					t.Errorf("%q has an empty translation", key)
				}
			}
		})
	}
}
