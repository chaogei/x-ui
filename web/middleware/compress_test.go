package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const assetsPrefix = "/assets/"

// compressEngine 挂一个只压 /assets/ 的引擎，并提供几条形态不同的路由。
func compressEngine(body string) *gin.Engine {
	engine := gin.New()
	engine.Use(CompressStatic(assetsPrefix))

	engine.GET("/assets/app.js", func(c *gin.Context) {
		c.Header("Content-Type", "text/javascript")
		c.String(http.StatusOK, body)
	})
	engine.GET("/assets/tiny.js", func(c *gin.Context) {
		c.Header("Content-Type", "text/javascript")
		c.Header("Content-Length", "3")
		c.String(http.StatusOK, "hi\n")
	})
	engine.GET("/assets/missing.js", func(c *gin.Context) {
		c.Header("Content-Type", "text/javascript")
		c.String(http.StatusNotFound, body)
	})
	engine.GET("/assets/precompressed.js", func(c *gin.Context) {
		c.Header("Content-Type", "text/javascript")
		c.Header("Content-Encoding", "br")
		c.String(http.StatusOK, body)
	})
	engine.GET("/xui/", func(c *gin.Context) {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusOK, body)
	})
	return engine
}

func compressibleBody() string {
	return strings.Repeat("export const answer = 42;\n", 400)
}

func do(engine *gin.Engine, path, acceptEncoding string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func gunzip(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	r, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("the body is not a gzip stream: %v", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read the gzip stream: %v", err)
	}
	return string(out)
}

func TestCompressStaticShrinksAssets(t *testing.T) {
	body := compressibleBody()
	rec := do(compressEngine(body), "/assets/app.js", "gzip, deflate, br")

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding", got)
	}
	// 压缩后的长度只有写完才知道，声明一个原始长度会让客户端提前断开。
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Errorf("Content-Length = %q, want it dropped for a compressed body", got)
	}
	if rec.Body.Len() >= len(body) {
		t.Errorf("the compressed body is %d bytes, no smaller than the original %d",
			rec.Body.Len(), len(body))
	}
	if got := gunzip(t, rec); got != body {
		t.Errorf("the decompressed body does not round-trip (%d vs %d bytes)", len(got), len(body))
	}
	// 类型必须保持原样，否则浏览器会拒绝执行这个脚本。
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("Content-Type = %q, want the original type preserved", got)
	}
}

func TestCompressStaticLeavesEverythingElseAlone(t *testing.T) {
	body := compressibleBody()
	engine := compressEngine(body)

	cases := map[string]struct {
		path           string
		acceptEncoding string
	}{
		"a page outside the asset prefix": {"/xui/", "gzip"},
		"a client that cannot decompress": {"/assets/app.js", ""},
		"a client that refuses gzip":      {"/assets/app.js", "gzip;q=0, deflate"},
		"a body below the threshold":      {"/assets/tiny.js", "gzip"},
		"a non-200 response":              {"/assets/missing.js", "gzip"},
		"an already-encoded response":     {"/assets/precompressed.js", "gzip"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(engine, tc.path, tc.acceptEncoding)
			if got := rec.Header().Get("Content-Encoding"); got == "gzip" {
				t.Fatalf("the response was compressed anyway (Content-Encoding = %q)", got)
			}
		})
	}
}

// 压缩不能改变响应的可见内容。
func TestCompressStaticPreservesTheBodyExactly(t *testing.T) {
	// 一份不怎么可压缩、且含多字节字符的正文。
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		sb.WriteString("每个字节都要原样回来 ")
		sb.WriteByte(byte('a' + i%26))
	}
	body := sb.String()

	rec := do(compressEngine(body), "/assets/app.js", "gzip")
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := gunzip(t, rec); got != body {
		t.Error("the decompressed body differs from what the handler wrote")
	}
}

// Range 与压缩不能同时成立：区间说的是原始字节，压缩之后没有意义。
func TestCompressStaticDropsRangeRequests(t *testing.T) {
	engine := gin.New()
	engine.Use(CompressStatic(assetsPrefix))
	var sawRange string
	engine.GET("/assets/app.js", func(c *gin.Context) {
		sawRange = c.GetHeader("Range")
		c.Header("Content-Type", "text/javascript")
		c.String(http.StatusOK, compressibleBody())
	})

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Range", "bytes=0-99")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if sawRange != "" {
		t.Errorf("the handler still saw Range: %q", sawRange)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want a whole 200 response", rec.Code)
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                            false,
		"deflate":                     false,
		"gzip":                        true,
		"gzip, deflate, br":           true,
		" GZIP ":                      true,
		"gzip;q=0":                    false,
		"gzip;q=0.0":                  false,
		"gzip;q=0.1":                  true,
		"deflate, gzip;q=0.5":         true,
		"identity;q=1, gzip;q=0":      false,
		"br;q=1.0, gzip;q=0.8, *;q=0": true,
	}
	for header, want := range cases {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
