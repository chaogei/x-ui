package locale

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 面板迁到 Vue 3 之后，界面上的每一个字都由前端查 boot.i18n 得到，而
// boot.i18n 就是这三份 TOML。前端的 t() 在查不到时原样回显 key —— 这个回退
// 是有意的（空白会让人以为是布局坏了，裸 key 一眼能定位该补哪条），
// 但代价是漏配的 key 不会报错，只会安静地把 "twofa_scan_hint" 印在界面上。
//
// TestEveryLanguageDefinesTheSameKeys 管的是三份词典彼此对齐；这条用例管的是
// 另一个方向：前端引用的 key 在词典里确实存在。两者都不能少。

const frontendSrcDir = "../frontend/src"

// i18nKeyLiteral 抓源码里所有形如 'foo_bar' / "foo_bar" 的 snake_case 字符串。
//
// 只认 snake_case 是因为它恰好就是本仓库的 key 命名规范
// （`<模块>_<字段>[_ph|_desc|_hint]`，见 README）。像 'UUID'、'flow'、
// 'xtls-rprx-vision' 这些标签本来就不进词典，也匹配不上。
//
// 不去解析 t(...) 的调用点：真正容易漏的恰恰是 forms.ts 里那些以数据形式
// 存在、由 ProtocolFields.vue 动态 t(field.label) 消费的 key，
// 语法上根本看不出来它们是 key。宁可多扫一些字符串，也不能漏掉这一类。
var i18nKeyLiteral = regexp.MustCompile(`['"]([a-z][a-z0-9]*(?:_[a-z0-9]+)+)['"]`)

// literalLabels 是"长得像 key 但故意不进词典"的字符串。
//
// 它们全是 sing-box 配置里的字段名本身。用户要拿这些名字去对照 sing-box 文档，
// 翻译反而帮倒忙，所以界面上原样显示，靠 t() 的回退透出去。
//
// 往这里加东西之前先想清楚：这是一个 sing-box/协议层面的标识符，还是一句
// 该被翻译的话？后者请去补三份 TOML。
var literalLabels = map[string]bool{
	"auth_timeout":            true,
	"congestion_control":      true,
	"domain_strategy":         true,
	"down_mbps":               true,
	"ignore_client_bandwidth": true,
	"ipv4_only":               true,
	"ipv6_only":               true,
	"new_reno":                true,
	"override_address":        true,
	"override_port":           true,
	"padding_scheme":          true,
	"persistent_keepalive":    true,
	"pre_shared_key":          true,
	"prefer_ipv4":             true,
	"prefer_ipv6":             true,
	"private_key":             true,
	"public_key":              true,
	"service_name":            true,
	"strict_mode":             true,
	"up_mbps":                 true,
	"zero_rtt_handshake":      true,
}

func TestFrontendReferencesOnlyDefinedKeys(t *testing.T) {
	files := translationFiles(t)
	defined := loadKeys(t, files["translate.zh_Hans.toml"])

	// key → 第一个引用它的文件，报错时直接给出位置。
	referenced := map[string]string{}
	err := filepath.WalkDir(frontendSrcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (!strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".vue")) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range i18nKeyLiteral.FindAllStringSubmatch(string(body), -1) {
			if _, seen := referenced[m[1]]; !seen {
				referenced[m[1]] = path
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", frontendSrcDir, err)
	}
	if len(referenced) == 0 {
		t.Fatalf("scanned %s and found no i18n keys at all; the scanner is broken", frontendSrcDir)
	}

	var missing []string
	for key := range referenced {
		if defined[key] == "" && !literalLabels[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		t.Errorf("the frontend uses i18n key %q (%s) but no translation defines it;\n"+
			"the panel will render the bare key on screen. Add it to all three "+
			"web/translation/*.toml files, or list it in literalLabels if it is a "+
			"sing-box field name that should stay untranslated.", key, referenced[key])
	}
}

// TestLiteralLabelsStayOutOfTheDictionary 让豁免名单跟着现实走。
//
// 一旦某个字段名真的被翻译了，它就该从名单里删掉——否则名单会慢慢退化成
// 一堆没人敢动的历史遗留，下一个人也就不再相信它。
func TestLiteralLabelsStayOutOfTheDictionary(t *testing.T) {
	files := translationFiles(t)
	defined := loadKeys(t, files["translate.zh_Hans.toml"])

	for key := range literalLabels {
		if defined[key] != "" {
			t.Errorf("%q is both in literalLabels and in the dictionary; "+
				"drop it from literalLabels so the parity check actually covers it", key)
		}
	}
}
