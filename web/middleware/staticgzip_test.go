package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// bundle 模拟一份前端产物：足够大、可压缩，和真正的 xui.js 一样。
var bundle = strings.Repeat("export const answer = 42;\n", 4000)

func gzipEngine(immutable bool) *gin.Engine {
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		// 真实的栈里 Cache-Control 由前一个中间件设置，压缩必须原样保留它。
		if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
			c.Header("Cache-Control", "max-age=31536000")
		}
	})
	engine.Use(StaticGzip("/assets/", immutable))
	engine.GET("/assets/dist/xui.js", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/javascript", []byte(bundle))
	})
	engine.GET("/assets/dist/tiny.css", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/css", []byte("body{}"))
	})
	engine.GET("/assets/dist/logo.png", func(c *gin.Context) {
		c.Data(http.StatusOK, "image/png", []byte(bundle))
	})
	engine.GET("/xui/inbound/list", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", []byte(bundle))
	})
	return engine
}

func fetch(engine *gin.Engine, method, path string, headers ...[2]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func gunzip(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("response is labelled gzip but does not decode: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	return string(plain)
}

const acceptsGzip = "gzip, deflate, br"

func TestStaticGzipCompressesTheBundle(t *testing.T) {
	rec := fetch(gzipEngine(true), http.MethodGet, "/assets/dist/xui.js?1.2.3",
		[2]string{"Accept-Encoding", acceptsGzip})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	// 客户端按 Content-Length 判断这一次响应有没有收完；它必须是压缩后的长度。
	// 先读，因为 gunzip 会把 recorder 的缓冲区消费掉。
	sent := rec.Body.Len()
	if sent >= len(bundle) {
		t.Errorf("the compressed body is %d bytes against %d uncompressed; nothing was gained", sent, len(bundle))
	}
	if got, want := rec.Header().Get("Content-Length"), strconv.Itoa(sent); got != want {
		t.Errorf("Content-Length = %q, want %q (the compressed length)", got, want)
	}
	if got := gunzip(t, rec); got != bundle {
		t.Errorf("the decompressed body is not the bundle (%d bytes vs %d)", len(got), len(bundle))
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q; a shared cache would hand the gzip copy to a client that cannot read it", got)
	}
}

// 版本号 query 是面板打破浏览器缓存的唯一手段，Cache-Control 是它一年不回源的
// 依据。压缩层不能把这两件事里的任何一件弄丢。
func TestStaticGzipKeepsCacheControl(t *testing.T) {
	for _, encoding := range []string{acceptsGzip, "identity"} {
		t.Run(encoding, func(t *testing.T) {
			rec := fetch(gzipEngine(true), http.MethodGet, "/assets/dist/xui.js?1.2.3",
				[2]string{"Accept-Encoding", encoding})
			if got := rec.Header().Get("Cache-Control"); got != "max-age=31536000" {
				t.Errorf("Cache-Control = %q, want it untouched", got)
			}
		})
	}
}

func TestStaticGzipLeavesTheRestAlone(t *testing.T) {
	engine := gzipEngine(true)

	cases := map[string]struct {
		method  string
		path    string
		headers [][2]string
	}{
		"a client that did not ask for gzip": {
			method: http.MethodGet, path: "/assets/dist/xui.js",
			headers: [][2]string{{"Accept-Encoding", "identity"}},
		},
		"a client that refuses gzip with q=0": {
			method: http.MethodGet, path: "/assets/dist/xui.js",
			headers: [][2]string{{"Accept-Encoding", "gzip;q=0, identity"}},
		},
		"a range request": {
			method: http.MethodGet, path: "/assets/dist/xui.js",
			headers: [][2]string{{"Accept-Encoding", acceptsGzip}, {"Range", "bytes=0-99"}},
		},
		"an already compressed content type": {
			method: http.MethodGet, path: "/assets/dist/logo.png",
			headers: [][2]string{{"Accept-Encoding", acceptsGzip}},
		},
		"a route outside the asset prefix": {
			method: http.MethodGet, path: "/xui/inbound/list",
			headers: [][2]string{{"Accept-Encoding", acceptsGzip}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := fetch(engine, tc.method, tc.path, tc.headers...)
			if enc := rec.Header().Get("Content-Encoding"); enc != "" {
				t.Fatalf("Content-Encoding = %q, want the response left uncompressed", enc)
			}
			if rec.Body.String() != bundle {
				t.Errorf("body is %d bytes, want the %d-byte original", rec.Body.Len(), len(bundle))
			}
		})
	}
}

// 比一个 MTU 还小的响应压完可能更大，而且省不出一个往返。
func TestStaticGzipSkipsTinyResponses(t *testing.T) {
	rec := fetch(gzipEngine(true), http.MethodGet, "/assets/dist/tiny.css",
		[2]string{"Accept-Encoding", acceptsGzip})

	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q for a 6-byte body", enc)
	}
	if rec.Body.String() != "body{}" {
		t.Errorf("body = %q, want it intact", rec.Body.String())
	}
}

// 缓冲响应最容易搞砸的就是错误路径：状态码和响应体都得原样穿过去。
func TestStaticGzipPassesErrorsThrough(t *testing.T) {
	engine := gin.New()
	engine.Use(StaticGzip("/assets/", true))
	engine.GET("/assets/missing.js", func(c *gin.Context) {
		c.Data(http.StatusNotFound, "text/plain", []byte("404 page not found"))
	})

	rec := fetch(engine, http.MethodGet, "/assets/missing.js", [2]string{"Accept-Encoding", acceptsGzip})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if rec.Body.String() != "404 page not found" {
		t.Errorf("body = %q, want the handler's", rec.Body.String())
	}
}

// 同一个 URL 连着取两次必须字节一致——缓存命中与未命中不能是两种结果。
func TestStaticGzipCacheServesTheSameBytes(t *testing.T) {
	engine := gzipEngine(true)

	first := fetch(engine, http.MethodGet, "/assets/dist/xui.js", [2]string{"Accept-Encoding", acceptsGzip})
	second := fetch(engine, http.MethodGet, "/assets/dist/xui.js", [2]string{"Accept-Encoding", acceptsGzip})

	if first.Body.String() != second.Body.String() {
		t.Error("two requests for the same asset produced different bytes")
	}
	if got := gunzip(t, second); got != bundle {
		t.Errorf("the cached copy decompresses to %d bytes, want %d", len(got), len(bundle))
	}
}

func TestGzipCacheRejectsAStaleEntry(t *testing.T) {
	cache := newGzipCache()
	cache.put("/assets/dist/xui.js", 100, []byte("compressed"))

	if _, ok := cache.get("/assets/dist/xui.js", 100); !ok {
		t.Fatal("the entry that was just stored is not readable")
	}
	// 长度对不上说明底下的文件已经不是当初压的那一份了。
	if _, ok := cache.get("/assets/dist/xui.js", 101); ok {
		t.Error("a cached copy of a different file length was handed out")
	}
}

func TestGzipCacheIsBounded(t *testing.T) {
	cache := newGzipCache()
	for i := 0; i < maxCachedAssets*2; i++ {
		cache.put("/assets/"+strconv.Itoa(i), 10, []byte("x"))
	}
	if len(cache.entries) > maxCachedAssets {
		t.Errorf("the cache holds %d entries, want at most %d", len(cache.entries), maxCachedAssets)
	}
}

// nil 缓存是调试模式的形态：每次都重新压，绝不复用。
func TestStaticGzipWithoutCacheStillCompresses(t *testing.T) {
	rec := fetch(gzipEngine(false), http.MethodGet, "/assets/dist/xui.js",
		[2]string{"Accept-Encoding", acceptsGzip})

	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	if got := gunzip(t, rec); got != bundle {
		t.Error("the uncached path returned something other than the bundle")
	}
}
