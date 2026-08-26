package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"testing"
)

// 前端产物是一个 1.2 MB 的单文件 bundle。它不压缩地走一遍网络，是首屏最大的
// 一笔开销，而且面板常见的部署位置离用户很远。
//
// 这些用例走完整的引擎栈，因此顺带钉住了另一件容易在压缩层被弄丢的事：
// assets 的 Cache-Control 与 URL 上的版本号 query（app.html 靠它破缓存）。
func TestE2EBundleIsServedCompressed(t *testing.T) {
	p := newPanel(t)

	embedded, err := assetsFS.ReadFile("assets/dist/xui.js")
	if err != nil {
		t.Fatalf("read the embedded bundle: %v", err)
	}

	resp := p.get("assets/dist/xui.js?9.9.9", [2]string{"Accept-Encoding", "gzip"})
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET the bundle = %d, want 200", resp.StatusCode)
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	if got, want := resp.Header.Get("Content-Length"), strconv.Itoa(len(body)); got != want {
		t.Errorf("Content-Length = %q but %q bytes arrived", got, want)
	}
	if resp.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q; a proxy would serve the gzip copy to clients that cannot read it",
			resp.Header.Get("Vary"))
	}
	// 版本号 query 决定浏览器什么时候重新拉产物，一年的 max-age 决定它在此之前
	// 不回源。压缩层自己应答，所以这个头很容易在改动中掉队。
	if cc := resp.Header.Get("Cache-Control"); cc != "max-age=31536000" {
		t.Errorf("Cache-Control = %q, want max-age=31536000", cc)
	}
	// 这是一台全新面板的第一个请求，所以 CSRF 中间件会在这条响应上建会话。
	// 压缩层自己下发响应头，任何在它之前设置的头都必须一起走出去 —— 丢了
	// 这个 Set-Cookie，用户就是"页面加载了但登录不上"。
	if resp.Header.Get("Set-Cookie") == "" {
		t.Error("the session cookie did not survive the compressed response")
	}

	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the body is labelled gzip but does not decode: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("decompress the bundle: %v", err)
	}
	if !bytes.Equal(plain, embedded) {
		t.Fatalf("the decompressed bundle is %d bytes, the embedded one is %d", len(plain), len(embedded))
	}
	if len(body) >= len(embedded)/2 {
		t.Errorf("gzip took the bundle from %d to %d bytes; that is not worth the CPU", len(embedded), len(body))
	}
}

// 不声明支持 gzip 的客户端必须照旧拿到原始字节。
func TestE2EBundleStillServesPlainBytes(t *testing.T) {
	p := newPanel(t)

	embedded, err := assetsFS.ReadFile("assets/dist/xui.css")
	if err != nil {
		t.Fatalf("read the embedded stylesheet: %v", err)
	}

	resp := p.get("assets/dist/xui.css?9.9.9", [2]string{"Accept-Encoding", "identity"})
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET the stylesheet = %d, want 200", resp.StatusCode)
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q for a client that asked for identity", enc)
	}
	if !bytes.Equal(body, embedded) {
		t.Errorf("the served stylesheet is %d bytes, the embedded one is %d", len(body), len(embedded))
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "max-age=31536000" {
		t.Errorf("Cache-Control = %q, want max-age=31536000", cc)
	}
}
