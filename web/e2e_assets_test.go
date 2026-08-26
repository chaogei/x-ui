package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

const bundlePath = "assets/dist/xui.js"

// TestE2EAssetsAreServedCompressed 走真正的静态路由验证压缩。
//
// 产物有 1.25 MiB。面板常常跑在按流量计费的小机器上，首屏差的就是这近 1 MB。
func TestE2EAssetsAreServedCompressed(t *testing.T) {
	p := newPanel(t)

	original, err := assetsFS.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read the embedded bundle: %v", err)
	}

	// 显式带上 Accept-Encoding，Go 的 Transport 就不会自动解压，
	// 于是能看到线上真实传输的字节。
	resp := p.get(bundlePath, [2]string{"Accept-Encoding", "gzip"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET the bundle returned %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := resp.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding", got)
	}
	wire := readBody(t, resp)
	if len(wire) >= len(original)/2 {
		t.Errorf("the compressed bundle is %d bytes against %d uncompressed; that is barely a saving",
			len(wire), len(original))
	}

	r, err := gzip.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("the served body is not a gzip stream: %v", err)
	}
	defer r.Close()
	decoded, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("decompress the bundle: %v", err)
	}
	if !bytes.Equal(decoded, original) {
		t.Error("the decompressed bundle differs from the embedded one")
	}

	// 不支持压缩的客户端拿到的仍然是原件。
	plain := p.get(bundlePath, [2]string{"Accept-Encoding", "identity"})
	if got := plain.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q for a client that asked for identity", got)
	}
	if body := readBody(t, plain); !bytes.Equal(body, original) {
		t.Error("the uncompressed response differs from the embedded bundle")
	}
}

// TestE2EDynamicResponsesAreNotCompressed 是那条边界的护栏。
//
// 页面与 API 响应里既有 CSRF token 这类机密，又有请求方能影响的内容。
// 把两者压进同一个流，压缩率就成了一条可测量的侧信道（BREACH）。
// assets/ 下没有这个问题：那些字节与请求无关。
func TestE2EDynamicResponsesAreNotCompressed(t *testing.T) {
	p := newPanel(t)
	p.login()

	for _, path := range []string{"", "xui/"} {
		resp := p.get(path, [2]string{"Accept-Encoding", "gzip"})
		body := readBody(t, resp)
		if got := resp.Header.Get("Content-Encoding"); got != "" {
			t.Errorf("GET %q came back with Content-Encoding %q; pages carry the CSRF token", path, got)
		}
		if len(body) == 0 {
			t.Errorf("GET %q returned an empty body", path)
		}
	}

	resp := p.postForm("xui/inbound/list", nil, [2]string{"Accept-Encoding", "gzip"})
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("an API response came back with Content-Encoding %q", got)
	}
	resp.Body.Close()
}
