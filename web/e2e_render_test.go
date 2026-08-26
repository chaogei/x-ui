package web

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 面板迁到 Vue 3 之后，Go 侧几乎不再产出可见内容：模板只是一层挂载 #app 的壳，
// 页面上的每一个字都由 web/assets/dist/xui.js 渲染。
//
// 这带来一类 Go 用例结构性看不见的故障：产物里任何一处运行时异常——组件 import
// 写错、boot 数据形状对不上、某个 antd API 在 4.x 改了名——都会让 Vue 挂载失败，
// 而服务端从头到尾都是对的：状态码 200，HTML 完整，CSRF token 也在。用户拿到的
// 是一张白页，测试全绿。
//
// 所以这里把真正的浏览器语义补回来：起 httptest 面板，取回四个页面的 HTML，
// 交给 jsdom 执行内嵌的产物，再断言渲染结果里确实有东西。
//
// 需要 node 与 web/frontend/node_modules/jsdom。两者缺一就跳过——没装 Node 的人
// 依然能 `go test ./...`，这是整个"产物提交进仓库"策略的前提。CI 里 Node 是装好的，
// 所以这条用例在那儿会真的跑。

// renderResult 是 scripts/render-smoke.mjs 的输出。
type renderResult struct {
	Page    string   `json:"page"`
	Mounted bool     `json:"mounted"`
	Text    string   `json:"text"`
	Inputs  int      `json:"inputs"`
	Buttons int      `json:"buttons"`
	Errors  []string `json:"errors"`
}

func TestE2EFrontendBundleRendersEveryPage(t *testing.T) {
	smoke := requireRenderSmoke(t)

	p := newPanel(t)

	cases := []struct {
		name string
		path string
		page string
		// wantText 是页面上必须出现的片段。挑的都是"只有这个视图渲染成功才会有"
		// 的字符串，而不是侧边栏那种四个页面共用的内容。
		wantText   []string
		needsLogin bool
		minInputs  int
	}{
		{
			name:      "login",
			path:      "",
			page:      "login",
			wantText:  []string{"two-factor"},
			minInputs: 2, // 用户名 + 密码
		},
		{
			name:       "status",
			path:       "xui/",
			page:       "status",
			wantText:   []string{"sing-box status", "load average", "tcp / udp connections"},
			needsLogin: true,
		},
		{
			name:       "inbounds",
			path:       "xui/inbounds",
			page:       "inbounds",
			wantText:   []string{"Inbound count", "protocol", "Transport"},
			needsLogin: true,
		},
		{
			name:       "setting",
			path:       "xui/setting",
			page:       "setting",
			wantText:   []string{"Panel listen port", "Subscription", "Telegram"},
			needsLogin: true,
			minInputs:  1,
		},
	}

	loggedIn := false
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsLogin && !loggedIn {
				p.login()
				loggedIn = true
			}

			// 固定 en-US：断言的是英文文案，跟着运行环境的 Accept-Language 走
			// 会让这条用例在装了中文 locale 的机器上莫名其妙地红。
			body := readBody(t, p.get(tc.path, [2]string{"Accept-Language", "en-US"}))

			res := smoke.run(t, body)

			for _, e := range res.Errors {
				t.Errorf("the bundle logged an error while rendering %s: %s", tc.name, e)
			}
			if res.Page != tc.page {
				t.Errorf("window.__XUI__.page = %q, want %q — the server told the frontend to mount the wrong view",
					res.Page, tc.page)
			}
			if !res.Mounted {
				t.Fatalf("#app is empty after the bundle ran: the Vue app did not mount, so %s is a blank page", tc.name)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(res.Text, want) {
					t.Errorf("rendered %s does not contain %q\ngot: %s", tc.name, want, truncate(res.Text, 600))
				}
			}
			if res.Inputs < tc.minInputs {
				t.Errorf("rendered %s has %d input elements, want at least %d", tc.name, res.Inputs, tc.minInputs)
			}
			if res.Buttons == 0 {
				t.Errorf("rendered %s has no buttons — nothing on the page is actionable", tc.name)
			}
		})
	}
}

// TestE2EFrontendUsesTheRequestLocale 确认 i18n 词典真的一路走到了渲染结果。
//
// 服务端把整本词典塞进 window.__XUI__.i18n，前端拿它查 key。中间任何一环断掉
// （tag 归一化、词典查找、前端 t() 的回退），页面都会退化成一堆裸 key，
// 而这在只看 HTML 的用例里同样是不可见的：外壳模板本来就不含任何文案。
func TestE2EFrontendUsesTheRequestLocale(t *testing.T) {
	smoke := requireRenderSmoke(t)

	p := newPanel(t)
	p.login()

	for _, tc := range []struct {
		lang string
		want string
	}{
		{"zh-Hans", "系统状态"},
		{"zh-Hant", "系統狀態"},
		{"en-US", "system status"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			body := readBody(t, p.get("xui/", [2]string{"Accept-Language", tc.lang}))
			res := smoke.run(t, body)
			if !res.Mounted {
				t.Fatalf("the Vue app did not mount for %s", tc.lang)
			}
			if !strings.Contains(res.Text, tc.want) {
				t.Errorf("page rendered for %s does not contain %q\ngot: %s",
					tc.lang, tc.want, truncate(res.Text, 400))
			}
			// 裸 key 漏到界面上是 i18n 断链最典型的样子。
			if strings.Contains(res.Text, "menu_system_status") {
				t.Errorf("page shows the raw i18n key menu_system_status instead of a translation")
			}
		})
	}
}

// renderSmoke 是 jsdom 渲染器的句柄。
type renderSmoke struct {
	node   string
	script string
}

// requireRenderSmoke 找到 node 与渲染脚本，缺任何一样就跳过整条用例。
//
// 一条会自己跳过的用例，最坏的下场是在 CI 里静默失效——环境改了没人发现，
// 护栏还挂在那儿假装在守。所以 CI 设 XUI_REQUIRE_RENDER_TEST=1，
// 此时缺依赖不再跳过而是直接失败。
func requireRenderSmoke(t *testing.T) *renderSmoke {
	t.Helper()

	required := os.Getenv("XUI_REQUIRE_RENDER_TEST") == "1"
	unavailable := func(format string, args ...interface{}) {
		if required {
			t.Fatalf("XUI_REQUIRE_RENDER_TEST=1 but the renderer is unavailable: "+format, args...)
		}
		t.Skipf(format, args...)
	}

	node, err := exec.LookPath("node")
	if err != nil {
		unavailable("node is not installed; run `npm --prefix web/frontend ci` " +
			"to exercise the frontend render check")
		return nil
	}

	script := filepath.Join("frontend", "scripts", "render-smoke.mjs")
	if _, err := os.Stat(script); err != nil {
		unavailable("%s is missing: %v", script, err)
		return nil
	}
	if _, err := os.Stat(filepath.Join("frontend", "node_modules", "jsdom")); err != nil {
		unavailable("web/frontend/node_modules/jsdom is missing; run `npm --prefix web/frontend ci` " +
			"to exercise the frontend render check")
		return nil
	}
	return &renderSmoke{node: node, script: script}
}

// run 把一页 HTML 喂给 jsdom 渲染器并解析结果。
func (r *renderSmoke) run(t *testing.T, page []byte) renderResult {
	t.Helper()

	// 产物有 1.6 MB，jsdom 解析 + Vue 首轮渲染在慢机器上要几秒。
	// 给足余量，但不要无限等：挂住比失败更难查。
	cmd := exec.Command(r.node, r.script)
	cmd.Stdin = bytes.NewReader(page)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the jsdom renderer: %v", err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the jsdom renderer failed: %v\nstderr: %s", err, truncate(stderr.String(), 2000))
		}
	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the jsdom renderer did not finish within 90s\nstderr: %s", truncate(stderr.String(), 2000))
	}

	var res renderResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &res); err != nil {
		t.Fatalf("the jsdom renderer produced unparseable output: %v\nstdout: %s\nstderr: %s",
			err, truncate(stdout.String(), 1000), truncate(stderr.String(), 1000))
	}
	return res
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
