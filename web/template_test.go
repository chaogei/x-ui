package web

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"

	"x-ui/web/locale"
)

// TestEmbeddedTemplatesCoverEveryFile 是一条便宜但价值极高的护栏。
//
// //go:embed html/* 默认会跳过以 `_` 或 `.` 开头的文件。仓库里
// html/xui/form/_tls.html 与 _transport.html 就叫这个名字，于是 release
// 构建里它们根本不在二进制里：每个协议表单都会在渲染时报
// `no such template "form/_tls"`，入站页返回 200 但 body 为空。
//
// 开发模式从磁盘读模板，所以本地怎么点都是好的——只有装出来的包才坏。
// 这条用例把"磁盘上有几个模板文件"与"二进制里有几个"钉在一起。
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
