package spec

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// protocolSpecJS 是前端协议元数据补丁的位置（相对本包目录）。
const protocolSpecJS = "../../../web/assets/js/model/protocol_spec.js"

// readFrontendPatch 解析 protocol_spec.js 里 `_frontendPatch` 的顶层协议键
// 及其 defaults() 函数体。
//
// 为什么用文本解析而不是跑一个 JS 引擎：这份补丁的形态被文件里的注释约定死了
// （每个协议一个 `key: { defaults() {...} }`），文本扫描足够可靠，
// 且不引入 JS 运行时依赖。解析不出来时测试会直接失败，不会静默放过。
func readFrontendPatch(t *testing.T) map[string]string {
	t.Helper()

	path := filepath.FromSlash(protocolSpecJS)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := stripJSComments(string(raw))

	start := strings.Index(src, "const _frontendPatch = {")
	if start < 0 {
		t.Fatalf("%s no longer declares _frontendPatch; update this test alongside the frontend", path)
	}
	body := src[start+len("const _frontendPatch = {"):]

	patch := make(map[string]string)
	var (
		depth        int
		inString     rune
		entryKey     string
		entryStart   int
		keyCandidate strings.Builder
	)
	for i, r := range body {
		if inString != 0 {
			if r == inString && (i == 0 || body[i-1] != '\\') {
				inString = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			inString = r
			continue
		case '{':
			if depth == 0 {
				// 进入一个协议条目：前面攒下的标识符就是它的 key。
				entryKey = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(keyCandidate.String()), ":"))
				entryStart = i + 1
			}
			depth++
			keyCandidate.Reset()
			continue
		case '}':
			depth--
			if depth < 0 {
				// _frontendPatch 对象本身闭合。
				return patch
			}
			if depth == 0 && entryKey != "" {
				patch[entryKey] = body[entryStart:i]
				entryKey = ""
			}
			keyCandidate.Reset()
			continue
		case ',':
			if depth == 0 {
				keyCandidate.Reset()
				continue
			}
		}
		if depth == 0 {
			keyCandidate.WriteRune(r)
		}
	}
	t.Fatalf("%s: _frontendPatch is not brace-balanced", path)
	return nil
}

// stripJSComments 去掉 JS 行注释与块注释，同时不碰字符串字面量里的 `//`
// （例如 URL）。注释里出现的协议名与字段名会让上面的扫描器认错 key，
// 所以解析前必须先清掉。
func stripJSComments(s string) string {
	var (
		out      strings.Builder
		inString byte
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString != 0 {
			out.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				out.WriteByte(s[i])
				continue
			}
			if c == inString {
				inString = 0
			}
			continue
		}
		switch {
		case c == '\'' || c == '"' || c == '`':
			inString = c
			out.WriteByte(c)
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i++
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// TestFrontendPatchCoversEveryProtocol 是前后端协议表的一致性闸门。
//
// 这两份表分处 Go 与 JS，没有编译期约束。少一个键，用户点"新建入站"时
// 前端会抛异常整页失效；多一个键，则是后端删协议时漏改的死代码。
func TestFrontendPatchCoversEveryProtocol(t *testing.T) {
	patch := readFrontendPatch(t)

	backend := make(map[string]bool, len(order))
	for _, s := range All() {
		backend[s.Key] = true
	}

	var missing, extra []string
	for key := range backend {
		if _, ok := patch[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range patch {
		if !backend[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("protocols registered in Go but missing from _frontendPatch: %v — the inbound dialog would throw", missing)
	}
	if len(extra) > 0 {
		t.Errorf("protocols in _frontendPatch with no Go registration: %v — dead frontend code", extra)
	}
	if len(patch) != len(backend) {
		t.Errorf("frontend has %d protocols, backend has %d", len(patch), len(backend))
	}
}

// TestFrontendDefaultsMatchUserSchema 检查每种协议的 defaults() 里确实建出了
// UserSchema 声明的凭证字段。
//
// 后端用 UserSchema 决定怎么展开多用户；前端用 defaults() 决定表单长什么样。
// 两者对不上时，用户填的字段不会被后端识别，症状是"保存成功但客户端连不上"。
func TestFrontendDefaultsMatchUserSchema(t *testing.T) {
	patch := readFrontendPatch(t)

	for _, s := range All() {
		if s.Users.Identifier == "" {
			continue
		}
		t.Run(s.Key, func(t *testing.T) {
			body := patch[s.Key]
			if s.Users.Container != "" && !strings.Contains(body, s.Users.Container+":") {
				t.Errorf("defaults() does not build the %q container declared by UserSchema", s.Users.Container)
			}
			if !strings.Contains(body, s.Users.Identifier+":") {
				t.Errorf("defaults() does not set the identifier field %q", s.Users.Identifier)
			}
			for _, cred := range s.Users.Credentials {
				if !strings.Contains(body, cred+":") {
					t.Errorf("defaults() does not set the credential field %q", cred)
				}
			}
		})
	}
}

// TestFrontendReadsBackendSpecs 确认前端没有把协议列表硬编码回去。
// 单一数据源的整个价值就在这一点上。
func TestFrontendReadsBackendSpecs(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(protocolSpecJS))
	if err != nil {
		t.Fatalf("read protocol_spec.js: %v", err)
	}
	src := string(raw)
	if !strings.Contains(src, "window.__PROTOCOL_SPECS__") {
		t.Error("protocol_spec.js must take its protocol list from the backend-injected window.__PROTOCOL_SPECS__")
	}
}

// TestRealityPublicKeyIsInTheFrontendModel 锁住 Reality 分享链接的修复。
//
// 修复前 RealityBlock 没有 public_key 字段，genVlessLink 读到 undefined，
// 生成的链接里 pbk 为空，客户端一律握手失败。
func TestRealityPublicKeyIsInTheFrontendModel(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash("../../../web/assets/js/model/core.js"))
	if err != nil {
		t.Fatalf("read core.js: %v", err)
	}
	src := string(raw)

	realityStart := strings.Index(src, "class RealityBlock")
	if realityStart < 0 {
		t.Fatal("core.js no longer defines RealityBlock")
	}
	// 取到下一个 class 声明为止，作为 RealityBlock 的定义范围。
	realityBody := src[realityStart:]
	if next := strings.Index(realityBody[len("class RealityBlock"):], "\nclass "); next >= 0 {
		realityBody = realityBody[:next+len("class RealityBlock")]
	}

	for _, want := range []string{"public_key", "private_key"} {
		if !strings.Contains(realityBody, want) {
			t.Errorf("RealityBlock must carry %q; the share link's pbk parameter depends on it", want)
		}
	}
	// 序列化两个方向都得带上，否则改一次入站就把 public_key 丢了。
	for _, method := range []string{"fromJson", "toJson"} {
		idx := strings.Index(realityBody, method)
		if idx < 0 {
			t.Errorf("RealityBlock has no %s", method)
			continue
		}
		if !strings.Contains(realityBody[idx:], "public_key") {
			t.Errorf("RealityBlock.%s drops public_key", method)
		}
	}
}
