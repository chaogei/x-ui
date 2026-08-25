// Package locale 提供面板的请求级 i18n 能力。
//
// 历史实现在 web.initI18n 里用一个包级 `var localizer` 承接每个请求的语言，
// 模板里的 `i18n` FuncMap 闭包读取同一个变量：两个并发请求只要 Accept-Language
// 不同就会互相覆盖，页面上会出现中英混排（且 -race 下可直接检出数据竞争）。
//
// 本包的约定：localizer 只存在于 gin.Context 与模板执行的 FuncMap 里，
// 任何时刻都不存在跨请求共享的可变语言状态。
package locale

import (
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"

	"x-ui/util/common"
)

const (
	// ContextKey 是 localizer 在 gin.Context 中的键。
	ContextKey = "localizer"
	// CookieName 是强制指定界面语言的 cookie 名（README 承诺的行为）。
	CookieName = "lang"
)

// Lang 描述一个可供用户选择的界面语言。
type Lang struct {
	// Code 是写入 cookie 的值，也是前端下拉框的 value。
	Code string
	// Name 是下拉框中展示的语言自称。
	Name string
	// Tag 是该 code 对应的 BCP-47 标签，用于构造 localizer。
	Tag string
}

// Supported 列出面板内置的三种语言。
// Code 采用用户熟悉的地区写法，Tag 对应 translation/*.toml 的文件名后缀。
var Supported = []Lang{
	{Code: "zh-CN", Name: "简体中文", Tag: "zh-Hans"},
	{Code: "zh-TW", Name: "繁體中文", Tag: "zh-Hant"},
	{Code: "en-US", Name: "English", Tag: "en-US"},
}

var (
	mu     sync.RWMutex
	bundle *i18n.Bundle
)

// Init 从给定文件系统的 root 目录加载全部 toml 词典。
// 可重复调用（测试里每次新建 server 都会调用），后一次覆盖前一次。
func Init(fsys fs.FS, root string) error {
	b := i18n.NewBundle(language.SimplifiedChinese)
	b.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		_, err = b.ParseMessageFileBytes(data, path)
		return err
	})
	if err != nil {
		return err
	}
	mu.Lock()
	bundle = b
	mu.Unlock()
	return nil
}

// Bundle 返回当前词典集合；未初始化时返回 nil。
func Bundle() *i18n.Bundle {
	mu.RLock()
	defer mu.RUnlock()
	return bundle
}

// normalizeCode 把 cookie 里的语言码映射到词典标签。
// 未知取值返回空串，调用方据此忽略 cookie 并回退到 Accept-Language。
func normalizeCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	for _, l := range Supported {
		if strings.EqualFold(l.Code, code) || strings.EqualFold(l.Tag, code) {
			return l.Tag
		}
	}
	// 容忍只写主语言的场景，例如 "en" / "zh"。
	switch strings.ToLower(strings.SplitN(code, "-", 2)[0]) {
	case "en":
		return "en-US"
	case "zh":
		return "zh-Hans"
	}
	return ""
}

// CookieLang 读取并规整请求中的语言 cookie。
func CookieLang(r *http.Request) string {
	if r == nil {
		return ""
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c == nil {
		return ""
	}
	return normalizeCode(c.Value)
}

// NewLocalizer 按「cookie 优先于 Accept-Language」的优先级构造 localizer。
func NewLocalizer(cookieTag, acceptLanguage string) *i18n.Localizer {
	b := Bundle()
	if b == nil {
		return nil
	}
	prefs := make([]string, 0, 2)
	if cookieTag != "" {
		prefs = append(prefs, cookieTag)
	}
	if acceptLanguage != "" {
		prefs = append(prefs, acceptLanguage)
	}
	return i18n.NewLocalizer(b, prefs...)
}

// Middleware 为每个请求构造独立的 localizer 并放入 gin.Context。
// 不写任何包级可变状态，因此并发请求之间完全隔离。
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		loc := NewLocalizer(CookieLang(c.Request), c.GetHeader("Accept-Language"))
		if loc != nil {
			c.Set(ContextKey, loc)
		}
		c.Next()
	}
}

// FromContext 取出当前请求的 localizer；缺失时返回 nil。
func FromContext(c *gin.Context) *i18n.Localizer {
	if c == nil {
		return nil
	}
	v, ok := c.Get(ContextKey)
	if !ok {
		return nil
	}
	loc, _ := v.(*i18n.Localizer)
	return loc
}

// CurrentCode 返回当前请求生效的语言 code（供模板高亮语言切换器）。
// 仅依据 cookie 判断；未设置 cookie 时返回空串表示"跟随浏览器"。
func CurrentCode(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	tag := CookieLang(c.Request)
	for _, l := range Supported {
		if l.Tag == tag {
			return l.Code
		}
	}
	return ""
}

// findParamNames 提取 message id 中的 `{{ .Name }}` 占位符名，
// 用于把模板里 `{{ i18n "key" "v1" }}` 的位置参数映射为命名参数。
func findParamNames(key string) []string {
	names := make([]string, 0)
	keyLen := len(key)
	for i := 0; i < keyLen-1; i++ {
		if key[i:i+2] != "{{" {
			continue
		}
		j := i + 2
		found := false
		for ; j < keyLen-1; j++ {
			if key[j:j+2] == "}}" {
				found = true
				break
			}
		}
		if found {
			names = append(names, key[i+3:j])
		}
	}
	return names
}

// Translate 按 messageID 翻译，可选带位置参数。
func Translate(loc *i18n.Localizer, key string, params ...string) (string, error) {
	names := findParamNames(key)
	if len(names) != len(params) {
		return "", common.NewError("find names:", names, "---------- params:", params, "---------- num not equal")
	}
	if loc == nil {
		return key, nil
	}
	data := map[string]interface{}{}
	for i := range names {
		data[names[i]] = params[i]
	}
	return loc.Localize(&i18n.LocalizeConfig{MessageID: key, TemplateData: data})
}

// TranslateOrKey 是 Translate 的宽松版本：出错时回退为 messageID 本身，
// 方便在页面上一眼看出漏配的 key，而不是整页 500。
func TranslateOrKey(loc *i18n.Localizer, key string) string {
	s, err := Translate(loc, key)
	if err != nil || s == "" {
		return key
	}
	return s
}

// FuncMap 返回绑定到指定 localizer 的模板函数集合。
// 每个请求拿到的都是新的闭包，不共享任何状态。
func FuncMap(loc *i18n.Localizer) template.FuncMap {
	return template.FuncMap{
		"i18n": func(key string, params ...string) (string, error) {
			return Translate(loc, key, params...)
		},
	}
}

// PlaceholderFuncMap 供模板解析阶段占位使用。
// html/template 要求解析时函数名已注册，实际实现在执行前会被 FuncMap 覆盖。
func PlaceholderFuncMap() template.FuncMap {
	return FuncMap(nil)
}
