package middleware

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// 前端产物是一个 1.2 MB 的单文件 bundle，压过之后约 380 KB。面板常见的部署
// 是一台境外小机器，首屏那三分之二的字节完全是白给的等待时间。
//
// 这里不引第三方压缩中间件，也不给整个引擎套压缩：面板的 API 响应都是几百
// 字节的 JSON，压它们只会白费 CPU；真正值得压的只有 /assets 下这两个文件。

// minGzipSize 是值得压缩的下限。比一个 MTU 还小的响应压完往往更大，
// 就算变小了也省不出一个网络往返。
const minGzipSize = 1024

// maxCachedAssets 给压缩结果缓存一个上界。生产模式下 assets 来自内嵌 FS，
// 条目数在编译期就定死了；这个上界是为调试模式下的磁盘目录准备的。
const maxCachedAssets = 64

// compressibleTypes 是值得压缩的 Content-Type 前缀。
// 图片、字体、压缩包本身已经是压缩格式，再压一遍只是烧 CPU。
var compressibleTypes = []string{
	"text/",
	"application/javascript",
	"application/json",
	"application/manifest+json",
	"application/wasm",
	"application/xml",
	"image/svg+xml",
}

// StaticGzip 对 prefix 下的静态资源做 gzip。
//
// immutable 说明这批文件在进程生命周期内不会变（生产模式下它们来自
// //go:embed）：此时压缩结果按路径缓存，每个文件整个进程只压一次，
// 并且用最高压缩级别——反正只付一次代价。调试模式下文件躺在磁盘上随时
// 会被重新构建，就不能缓存，级别也降回默认值。
//
// 中间件不自己应答，而是让原来的文件处理器照常跑完再压它的输出。这样
// ETag / If-Modified-Since / 404 这些语义一条都不用重新实现，Cache-Control
// 也还是由上游那个中间件按 URL 前缀设置的（见 web.initRouter）。
func StaticGzip(prefix string, immutable bool) gin.HandlerFunc {
	level := gzip.DefaultCompression
	if immutable {
		level = gzip.BestCompression
	}
	var cache *gzipCache
	if immutable {
		cache = newGzipCache()
	}

	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, prefix) {
			c.Next()
			return
		}
		// 无论这一次压不压，只要是同一个 URL 能有两种编码，缓存就必须按
		// Accept-Encoding 分桶，否则中间的代理会把 gzip 的那份喂给不支持
		// gzip 的客户端。
		c.Writer.Header().Set("Vary", "Accept-Encoding")

		if !wantsGzip(c.Request) {
			c.Next()
			return
		}

		original := c.Writer
		buffered := &bufferedWriter{
			ResponseWriter: original,
			path:           c.Request.URL.Path,
			status:         http.StatusOK,
		}
		c.Writer = buffered
		// 用 defer 而不是顺序执行：处理器 panic 时缓冲区里的内容仍然要发出去，
		// 否则客户端等到的是一个没有任何响应就断掉的连接。
		defer func() {
			c.Writer = original
			emit(original, buffered, cache, level)
		}()
		c.Next()
	}
}

func wantsGzip(r *http.Request) bool {
	// HEAD 不带响应体，压不出东西来，却会让 Content-Length 与真正 GET 到的
	// 长度对不上。其余方法根本不会落到静态文件处理器上。
	if r.Method != http.MethodGet {
		return false
	}
	// Range 请求的字节区间是对未压缩内容说的，压完就对不上了。
	// 这条路径本来也只有断点续传的下载器会走。
	if r.Header.Get("Range") != "" {
		return false
	}
	for _, enc := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		// "gzip;q=0" 是显式拒绝，不能当成支持。
		name, params, _ := strings.Cut(strings.TrimSpace(enc), ";")
		if name != "gzip" {
			continue
		}
		return !strings.Contains(strings.ReplaceAll(params, " ", ""), "q=0")
	}
	return false
}

// emit 把缓冲下来的响应写回真正的 ResponseWriter，能压则压。
func emit(w gin.ResponseWriter, buffered *bufferedWriter, cache *gzipCache, level int) {
	body := buffered.body.Bytes()
	header := w.Header()

	if buffered.status != http.StatusOK ||
		len(body) < minGzipSize ||
		header.Get("Content-Encoding") != "" ||
		!compressible(header.Get("Content-Type")) {
		writeRaw(w, buffered.status, body)
		return
	}

	compressed, ok := cache.get(buffered.path, len(body))
	if !ok {
		var err error
		if compressed, err = gzipBytes(body, level); err != nil {
			writeRaw(w, buffered.status, body)
			return
		}
		cache.put(buffered.path, len(body), compressed)
	}
	// 压不动的内容（已经压过的格式混在文本 Content-Type 里）原样发出去。
	if len(compressed) >= len(body) {
		writeRaw(w, buffered.status, body)
		return
	}

	header.Set("Content-Encoding", "gzip")
	// 区间是按未压缩字节算的，这条响应不再支持它。
	header.Del("Accept-Ranges")
	header.Set("Content-Length", strconv.Itoa(len(compressed)))
	w.WriteHeader(buffered.status)
	_, _ = w.Write(compressed)
}

func writeRaw(w gin.ResponseWriter, status int, body []byte) {
	if len(body) > 0 {
		// 文件处理器写的那个 Content-Length 是对的，但走到这里的可能是
		// 一条错误响应，长度得按缓冲区重算。
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	}
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

func compressible(contentType string) bool {
	mime, _, _ := strings.Cut(contentType, ";")
	mime = strings.TrimSpace(strings.ToLower(mime))
	for _, prefix := range compressibleTypes {
		if strings.HasPrefix(mime, prefix) {
			return true
		}
	}
	return false
}

func gzipBytes(body []byte, level int) ([]byte, error) {
	out := bytes.NewBuffer(make([]byte, 0, len(body)/3))
	zw, err := gzip.NewWriterLevel(out, level)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(body); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// bufferedWriter 把处理器的输出接住，等 emit 决定怎么发。
//
// 它必须完整替身 gin.ResponseWriter：内嵌的那个仍然提供 Header()，所以处理器
// 设置的头会直接落在真正的响应头上，而状态码和响应体被拦下来。
type bufferedWriter struct {
	gin.ResponseWriter

	path   string
	status int
	body   bytes.Buffer
}

func (w *bufferedWriter) WriteHeader(status int) { w.status = status }

// WriteHeaderNow 是 gin 用来强制下发响应头的口子。缓冲期间下发就前功尽弃了。
func (w *bufferedWriter) WriteHeaderNow() {}

func (w *bufferedWriter) Write(b []byte) (int, error) { return w.body.Write(b) }

func (w *bufferedWriter) WriteString(s string) (int, error) { return w.body.WriteString(s) }

// Flush 同理：缓冲期间不能真的把字节推给客户端。静态文件处理器不会调它。
func (w *bufferedWriter) Flush() {}

func (w *bufferedWriter) Status() int { return w.status }

func (w *bufferedWriter) Size() int { return w.body.Len() }

func (w *bufferedWriter) Written() bool { return false }

// gzipCache 按路径缓存压缩结果。
//
// 用未压缩长度做一致性校验：内嵌 FS 里的文件不会变，这一项只是防止缓存
// 被喂进一条本不属于它的响应（比如同一路径先 404 后 200）。
type gzipCache struct {
	mu      sync.RWMutex
	entries map[string]gzipEntry
}

type gzipEntry struct {
	rawLen int
	body   []byte
}

func newGzipCache() *gzipCache {
	return &gzipCache{entries: make(map[string]gzipEntry)}
}

func (c *gzipCache) get(path string, rawLen int) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[path]
	if !ok || entry.rawLen != rawLen {
		return nil, false
	}
	return entry.body, true
}

func (c *gzipCache) put(path string, rawLen int, body []byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[path]; !exists && len(c.entries) >= maxCachedAssets {
		return
	}
	c.entries[path] = gzipEntry{rawLen: rawLen, body: body}
}
