package middleware

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// 静态资源的 gzip 压缩。
//
// 只压缩静态资源，不压缩页面和 API 响应 —— 这是一个有意的边界，不是偷懒：
// 压缩一份既含机密（CSRF token、订阅 token）又含请求方可影响内容的响应，
// 会把压缩率变成一条侧信道（BREACH）。而 assets/ 下的东西每个字节都一样，
// 与请求无关，没有这个问题。
//
// 收益也集中在这里：Vite 产物 1.25 MiB，gzip 之后约 385 KB。面板通常跑在
// 一台按流量计费的小机器上，首屏差着将近 1 MB。
const (
	// compressMinSize 以下不压缩：几百字节的东西压完往往更大，
	// 而且还要多一次 CPU 与一个 gzip 头。
	compressMinSize = 1024
	// compressLevel 取默认级别。BestCompression 在这些产物上只多省个位数
	// 百分比，却要多花几倍 CPU；面板的资源是每次冷加载现压的。
	compressLevel = gzip.DefaultCompression
)

var gzipWriterPool = sync.Pool{
	New: func() interface{} {
		w, _ := gzip.NewWriterLevel(nil, compressLevel)
		return w
	},
}

// CompressStatic 对 prefix 之下的静态资源做 gzip 压缩。
//
// prefix 为空时中间件退化成一个直接放行的空操作。
func CompressStatic(prefix string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if prefix == "" || !strings.HasPrefix(c.Request.URL.Path, prefix) {
			c.Next()
			return
		}
		if !acceptsGzip(c.Request.Header.Get("Accept-Encoding")) {
			c.Next()
			return
		}
		// 同一个 URL 在压与不压两种形态之间切换，中间的缓存必须按
		// Accept-Encoding 分桶，否则会把 gzip 的响应喂给不支持的客户端。
		c.Writer.Header().Add("Vary", "Accept-Encoding")

		// 断点续传与压缩不能同时成立：Range 说的是原始字节区间，
		// 压缩之后那个区间没有意义。内嵌资源都是几百 KB 的整体加载，
		// 丢掉 Range 比返回一段无法解压的字节安全得多。
		c.Request.Header.Del("Range")

		gz := gzipWriterPool.Get().(*gzip.Writer)
		writer := &gzipResponseWriter{ResponseWriter: c.Writer, gz: gz}
		c.Writer = writer
		defer func() {
			writer.close()
			gzipWriterPool.Put(gz)
			c.Writer = writer.ResponseWriter
		}()
		c.Next()
	}
}

// acceptsGzip 解析 Accept-Encoding，认得显式的 gzip;q=0 拒绝。
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		for _, param := range fields[1:] {
			param = strings.TrimSpace(param)
			if !strings.HasPrefix(param, "q=") {
				continue
			}
			if q, err := strconv.ParseFloat(param[2:], 64); err == nil && q == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// gzipResponseWriter 在响应真正开始写的时候才决定压不压。
//
// 决策必须推迟到 WriteHeader：那时才知道状态码、Content-Type 与
// Content-Length，而这三样决定了压缩是否合适。
type gzipResponseWriter struct {
	gin.ResponseWriter

	gz          *gzip.Writer
	compressing bool
	decided     bool
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	w.decide(status)
	w.ResponseWriter.WriteHeader(status)
}

// decide 判断这个响应该不该压，并相应改写响应头。只执行一次。
func (w *gzipResponseWriter) decide(status int) {
	if w.decided {
		return
	}
	w.decided = true

	header := w.ResponseWriter.Header()
	switch {
	case status != http.StatusOK:
		// 304 没有 body，206 是区间响应，其余非 200 基本是短错误页。
		return
	case header.Get("Content-Encoding") != "":
		// 已经压过了（预压缩资源），不要套第二层。
		return
	case header.Get("Content-Type") == "":
		// 没有显式类型时，net/http 会拿正文的头 512 字节去嗅探。
		// 写进去的是 gzip 流的话，嗅出来的是 application/x-gzip。
		return
	}
	if n, err := strconv.Atoi(header.Get("Content-Length")); err == nil && n < compressMinSize {
		return
	}

	// 压缩后的长度此刻还不知道，留着原来的 Content-Length 会让客户端
	// 在读满声明的字节数之前就断开。
	header.Del("Content-Length")
	header.Set("Content-Encoding", "gzip")
	w.gz.Reset(w.ResponseWriter)
	w.compressing = true
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	w.decide(http.StatusOK)
	if !w.compressing {
		return w.ResponseWriter.Write(data)
	}
	// 返回写入的原始字节数：调用方按自己交出去的长度记账。
	w.ResponseWriter.WriteHeaderNow()
	return w.gz.Write(data)
}

func (w *gzipResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *gzipResponseWriter) Flush() {
	if w.compressing {
		_ = w.gz.Flush()
	}
	w.ResponseWriter.Flush()
}

// close 收尾 gzip 流。没有压缩时什么都不做。
func (w *gzipResponseWriter) close() {
	if !w.compressing {
		return
	}
	w.compressing = false
	_ = w.gz.Close()
}
