package web

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"

	"x-ui/web/locale"
)

// TestEmbeddedTemplatesCoverEveryFile 把"磁盘上有几个模板文件"与"二进制里有几个"
// 钉在一起。//go:embed html/* 默认会跳过以 `_` 或 `.` 开头的文件，而开发模式是从
// 磁盘读模板的 —— 本地怎么点都是好的，只有装出来的包才坏。
func TestEmbeddedTemplatesCoverEveryFile(t *testing.T) {
	embedded := make(map[string]bool)
	err := fs.WalkDir(htmlFS, "html", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			embedded[p] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded html: %v", err)
	}

	// os.DirFS 走的是真实磁盘，不受 embed 规则影响。
	var missing []string
	err = fs.WalkDir(diskFS(t), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		if !embedded[path.Join("html", p)] {
			missing = append(missing, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk html directory: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("these templates exist on disk but not in the binary: %v\n"+
			"the //go:embed directive needs the all: prefix to include _-prefixed files", missing)
	}
}

// TestEveryTemplateReferenceResolves 解析全部模板并检查交叉引用闭合。
//
// html/template 只在执行时才报"no such template"，而面板的错误处理是
// 记一条 warning 然后返回空 body —— 用户看到的是一个白页，日志在别处。
// 这里在测试期把所有引用一次性走一遍。
func TestEveryTemplateReferenceResolves(t *testing.T) {
	s := &Server{}
	tpl, err := s.getHtmlTemplate(locale.PlaceholderFuncMap())
	if err != nil {
		t.Fatalf("parse embedded templates: %v", err)
	}

	defined := make(map[string]bool)
	for _, sub := range tpl.Templates() {
		defined[sub.Name()] = true
	}

	// 逐个模板扫描 {{template "name"}} 引用。
	for _, sub := range tpl.Templates() {
		if sub.Tree == nil || sub.Tree.Root == nil {
			continue
		}
		for _, ref := range templateRefs(sub.Tree.Root.String()) {
			if !defined[ref] {
				t.Errorf("template %q references %q, which is not defined anywhere", sub.Name(), ref)
			}
		}
	}
}

// TestFrontendBundleIsEmbedded 是 Vue 3 迁移之后最重要的一条护栏。
//
// 模板已经退化成一层挂载 #app 的壳，页面上所有内容都由 assets/dist/xui.js 渲染。
// 只要有人忘了跑 `npm run build`（或者把产物加进了 .gitignore），二进制照样编译、
// 页面照样返回 200，但用户看到的是一张白页。这里直接对着 embed 出来的文件系统检查
// 产物存在且不是占位符。
func TestFrontendBundleIsEmbedded(t *testing.T) {
	for _, tc := range []struct {
		name    string
		minSize int
	}{
		{"assets/dist/xui.js", 100 * 1024},
		{"assets/dist/xui.css", 1024},
	} {
		data, err := assetsFS.ReadFile(tc.name)
		if err != nil {
			t.Fatalf("%s is not in the binary (did you run `npm --prefix web/frontend run build`?): %v", tc.name, err)
		}
		if len(data) < tc.minSize {
			t.Errorf("%s is only %d bytes, expected at least %d — that looks like a stub, not a real Vite build",
				tc.name, len(data), tc.minSize)
		}
	}
}

// TestNoVue2AssetsRemain 防止 Vue 2 / Ant Design Vue 1.x 悄悄回到二进制里。
//
// 两者都已 EOL。迁移之后 assets/ 下只应该有 Vite 产物；任何 vue@2 / antd 1.7
// 的目录或者 CDN 引用都说明迁移被回退了一半。
func TestNoVue2AssetsRemain(t *testing.T) {
	banned := []string{"vue@2", "ant-design-vue@1", "moment", "qs/", "uri/", "base64/"}
	err := fs.WalkDir(assetsFS, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, b := range banned {
			if strings.Contains(p+"/", "/"+b) {
				t.Errorf("legacy asset %q is still embedded in the binary", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded assets: %v", err)
	}

	// 模板里也不能再有指向 Vue 2 的 <script>。
	err = fs.WalkDir(htmlFS, "html", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := htmlFS.ReadFile(p)
		if err != nil {
			return err
		}
		for _, b := range []string{"vue@2", "ant-design-vue@1", "vue.min.js", "antd.min.js"} {
			if strings.Contains(string(body), b) {
				t.Errorf("template %q still references %q", p, b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded html: %v", err)
	}
}

// templateRefs 从模板源码里抓出 {{template "x"}} / {{ template "x" . }} 的名字。
func templateRefs(src string) []string {
	var refs []string
	rest := src
	for {
		idx := strings.Index(rest, `{{template "`)
		alt := strings.Index(rest, `{{ template "`)
		open := len(`{{template "`)
		if idx < 0 || (alt >= 0 && alt < idx) {
			idx = alt
			open = len(`{{ template "`)
		}
		if idx < 0 {
			return refs
		}
		rest = rest[idx+open:]
		end := strings.Index(rest, `"`)
		if end < 0 {
			return refs
		}
		refs = append(refs, rest[:end])
		rest = rest[end:]
	}
}

func diskFS(t *testing.T) fs.FS {
	t.Helper()
	return os.DirFS("html")
}
