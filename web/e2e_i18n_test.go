package web

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"x-ui/web/locale"
)

// 前端是 Vue 3 单页应用，可见文案由 JS 在浏览器里渲染，服务端返回的 HTML 里
// 没有翻译好的 placeholder 可抓。语言选择依然完全发生在服务端：它把整本词典
// 注入 window.__XUI__.i18n，前端只负责查表。
//
// 所以这里改成断言注入的词典本身。挑 "username" 这一条是因为三种语言的译文
// 互不相同，且 `"username":` 这个前缀在 JSON 里唯一，不会与别处的同名字段撞上。
var usernameEntries = map[string]string{
	"en-US": `"username":"username"`,
	"zh-CN": `"username":"用户名"`,
	"zh-TW": `"username":"使用者名稱"`,
}

// languageOf 根据页面注入的 i18n 词典判定生效语言。
func languageOf(body string) string {
	for code, marker := range usernameEntries {
		if strings.Contains(body, marker) {
			return code
		}
	}
	return "unknown"
}

func TestE2EAcceptLanguageSelectsTheLocale(t *testing.T) {
	p := newPanel(t)

	cases := map[string]string{
		"en-US":                   "en-US",
		"en":                      "en-US",
		"en-GB,en;q=0.9":          "en-US",
		"zh-CN,zh;q=0.9":          "zh-CN",
		"zh-TW":                   "zh-TW",
		"zh-Hant":                 "zh-TW",
		"fr-FR,fr;q=0.9,en;q=0.5": "en-US",
	}
	for header, want := range cases {
		t.Run(header, func(t *testing.T) {
			body := string(readBody(t, p.get("", [2]string{"Accept-Language", header})))
			if got := languageOf(body); got != want {
				t.Errorf("Accept-Language %q rendered %s, want %s", header, got, want)
			}
		})
	}
}

// TestE2ELangCookieBeatsAcceptLanguage 覆盖 README 承诺过、但此前从未实现的
// `lang` cookie：用户在浏览器语言之外显式选了什么，就得渲染什么。
func TestE2ELangCookieBeatsAcceptLanguage(t *testing.T) {
	p := newPanel(t)

	cases := map[string]string{
		"en-US":   "en-US",
		"zh-CN":   "zh-CN",
		"zh-TW":   "zh-TW",
		"en":      "en-US",
		"zh":      "zh-CN",
		"zh-Hant": "zh-TW",
	}
	for cookieValue, want := range cases {
		t.Run(cookieValue, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, p.url(""), nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			// Accept-Language 故意与 cookie 相左：cookie 必须赢。
			req.Header.Set("Accept-Language", "zh-CN")
			req.AddCookie(&http.Cookie{Name: locale.CookieName, Value: cookieValue})

			body := string(readBody(t, p.do(req)))
			if got := languageOf(body); got != want {
				t.Errorf("lang cookie %q rendered %s, want %s", cookieValue, got, want)
			}
		})
	}
}

// TestE2EUnknownLangCookieFallsBack 无法识别的 cookie 值不能把页面渲染坏。
func TestE2EUnknownLangCookieFallsBack(t *testing.T) {
	p := newPanel(t)

	for _, value := range []string{"", "klingon", "../../etc/passwd", "xx-YY"} {
		t.Run(value, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, p.url(""), nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Accept-Language", "en-US")
			req.AddCookie(&http.Cookie{Name: locale.CookieName, Value: value})

			body := string(readBody(t, p.do(req)))
			if got := languageOf(body); got != "en-US" {
				t.Errorf("an unrecognised lang cookie %q rendered %s, want a fallback to Accept-Language (en-US)", value, got)
			}
		})
	}
}

// TestE2EConcurrentLanguagesDoNotMix 是 M-1 的回归护栏。
//
// 旧实现把 localizer 存在包级变量里，模板的 i18n 闭包读同一个变量：
// 两个语言不同的并发请求会互相覆盖，页面上出现中英混排。
// 这条用例在 -race 下同时也能直接检出那处数据竞争。
func TestE2EConcurrentLanguagesDoNotMix(t *testing.T) {
	p := newPanel(t)

	langs := []struct{ header, want string }{
		{"en-US", "en-US"},
		{"zh-CN", "zh-CN"},
		{"zh-TW", "zh-TW"},
	}

	var wg sync.WaitGroup
	for round := 0; round < 12; round++ {
		for _, lang := range langs {
			wg.Add(1)
			go func(header, want string) {
				defer wg.Done()
				body := string(readBody(t, p.get("", [2]string{"Accept-Language", header})))
				if got := languageOf(body); got != want {
					t.Errorf("a concurrent %s request rendered %s", header, got)
				}
				// 一个页面里只应注入一种语言的词典。
				variants := 0
				for _, marker := range usernameEntries {
					if strings.Contains(body, marker) {
						variants++
					}
				}
				if variants > 1 {
					t.Errorf("a %s page mixes %d languages", header, variants)
				}
			}(lang.header, lang.want)
		}
	}
	wg.Wait()
}

// TestE2ELanguageSwitcherIsRendered 侧边栏得真有个切换入口，
// 否则 lang cookie 只有会改 devtools 的人用得上。
//
// 可选语言表由服务端注入（window.__XUI__.languages），前端照着渲染菜单；
// 这里断言注入的那份数据完整，等价于断言菜单不缺项。
func TestE2ELanguageSwitcherIsRendered(t *testing.T) {
	p := newPanel(t)
	p.login()

	body := string(readBody(t, p.get("xui/")))
	for _, lang := range locale.Supported {
		if !strings.Contains(body, lang.Name) {
			t.Errorf("the language switcher does not offer %q (%s)", lang.Name, lang.Code)
		}
		// 菜单项的 key 是 code，缺了它点击之后写不出正确的 lang cookie。
		if !strings.Contains(body, `"Code":"`+lang.Code+`"`) {
			t.Errorf("the injected language list has no code for %q", lang.Name)
		}
	}
}

// TestE2EOperationMessagesAreLocalized 覆盖 jsonMsg 的成功/失败后缀。
// 修复前这两个后缀是硬编码的简体中文，英文界面的用户会看到
// "add failed: ..." 之外的中文夹杂。
func TestE2EOperationMessagesAreLocalized(t *testing.T) {
	p := newPanel(t)
	p.login()

	badInbound := inboundForm(20040, "vmess", `[not json`)

	english := p.decode(p.postForm("xui/inbound/add", badInbound, [2]string{"Accept-Language", "en-US"}))
	if english.Success {
		t.Fatal("the malformed inbound was accepted")
	}
	if strings.ContainsAny(english.Msg, "失败成功添加") {
		t.Errorf("an English client got a message with Chinese in it: %q", english.Msg)
	}

	chinese := p.decode(p.postForm("xui/inbound/add", badInbound, [2]string{"Accept-Language", "zh-CN"}))
	if chinese.Success {
		t.Fatal("the malformed inbound was accepted")
	}
	if english.Msg == chinese.Msg {
		t.Errorf("the English and Chinese clients got the identical message %q; the toast is not localized", english.Msg)
	}
}
