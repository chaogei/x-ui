package web

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"x-ui/web/locale"
)

// 登录页用户名输入框的 placeholder 就是 {{ i18n "username" }} 的渲染结果。
// 挑这个位置是因为它唯一：页面里别处的 "username" 是 Vue 的 v-model 绑定，
// 不随语言变化，拿来判定语言会一直误报。
var usernamePlaceholders = map[string]string{
	"en-US": `placeholder='username'`,
	"zh-CN": `placeholder='用户名'`,
	"zh-TW": `placeholder='使用者名稱'`,
}

// languageOf 根据登录页的 placeholder 判定页面语言。
func languageOf(body string) string {
	for code, marker := range usernamePlaceholders {
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
				// 一个页面里只应出现一种语言的用户名 placeholder。
				variants := 0
				for _, marker := range usernamePlaceholders {
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
func TestE2ELanguageSwitcherIsRendered(t *testing.T) {
	p := newPanel(t)
	p.login()

	body := string(readBody(t, p.get("xui/")))
	for _, lang := range locale.Supported {
		if !strings.Contains(body, lang.Name) {
			t.Errorf("the language switcher does not offer %q (%s)", lang.Name, lang.Code)
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
