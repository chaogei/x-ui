package singbox

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"

	"x-ui/core/singbox/statspb"
)

/*
这个文件用一台真的 gRPC 服务器覆盖 statsClient 的生命周期。

用假 client 桩测不出这里真正会出问题的地方：连接是不是被复用、
Close 之后 socket 有没有真的还回去、请求带没带 deadline。
这三件事错了都不会让面板报错，只会让它慢慢漏 socket 或者在
内核卡死时把 cron goroutine 一起挂住。
*/

// statsWireCodec 是服务端侧的编解码。
//
// statspb 的消息刻意不实现 proto.Message（见该包的包注释），
// 所以服务端也得用同一套手写线格式，否则 gRPC 默认的 proto codec 会拒收。
type statsWireCodec struct{}

func (statsWireCodec) Name() string { return "proto" }

func (statsWireCodec) Marshal(v any) ([]byte, error) {
	resp, ok := v.(*statspb.QueryStatsResponse)
	if !ok {
		return nil, fmt.Errorf("test codec cannot marshal %T", v)
	}
	var b []byte
	for _, s := range resp.Stat {
		var inner []byte
		inner = protowire.AppendTag(inner, 1, protowire.BytesType)
		inner = protowire.AppendString(inner, s.Name)
		inner = protowire.AppendTag(inner, 2, protowire.VarintType)
		inner = protowire.AppendVarint(inner, uint64(s.Value))

		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, inner)
	}
	return b, nil
}

func (statsWireCodec) Unmarshal(data []byte, v any) error {
	req, ok := v.(*statspb.QueryStatsRequest)
	if !ok {
		return fmt.Errorf("test codec cannot unmarshal into %T", v)
	}
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		data = data[n:]
		size := protowire.ConsumeFieldValue(num, typ, data)
		if size < 0 {
			return protowire.ParseError(size)
		}
		if num == 2 && typ == protowire.VarintType {
			value, vn := protowire.ConsumeVarint(data)
			if vn < 0 {
				return protowire.ParseError(vn)
			}
			req.Reset_ = value != 0
		}
		data = data[size:]
	}
	return nil
}

type statsHandler interface {
	QueryStats(context.Context, *statspb.QueryStatsRequest) (*statspb.QueryStatsResponse, error)
}

// statsServiceDesc 手工声明服务，服务名与 sing-box 注册的完全一致。
var statsServiceDesc = grpc.ServiceDesc{
	ServiceName: "v2ray.core.app.stats.command.StatsService",
	HandlerType: (*statsHandler)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "QueryStats",
		Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			req := &statspb.QueryStatsRequest{}
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(statsHandler).QueryStats(ctx, req)
		},
	}},
}

// fakeStats 记录服务端观察到的每一次调用。
type fakeStats struct {
	mu             sync.Mutex
	calls          int
	sawReset       bool
	sawDeadline    bool
	deadlineWithin time.Duration
}

func (f *fakeStats) QueryStats(ctx context.Context, req *statspb.QueryStatsRequest) (*statspb.QueryStatsResponse, error) {
	f.mu.Lock()
	f.calls++
	f.sawReset = req.Reset_
	if deadline, ok := ctx.Deadline(); ok {
		f.sawDeadline = true
		f.deadlineWithin = time.Until(deadline)
	}
	f.mu.Unlock()

	return &statspb.QueryStatsResponse{Stat: []*statspb.Stat{
		{Name: "inbound>>>inbound-443-vmess>>>traffic>>>uplink", Value: 12},
		{Name: "inbound>>>inbound-443-vmess>>>traffic>>>downlink", Value: 34},
		{Name: "user>>>alice@example.com>>>traffic>>>uplink", Value: 7},
	}}, nil
}

func (f *fakeStats) snapshot() (calls int, reset, deadline bool, within time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.sawReset, f.sawDeadline, f.deadlineWithin
}

// countingListener 数进出的 TCP 连接，用来证明连接确实被复用且确实被归还。
type countingListener struct {
	net.Listener
	mu       sync.Mutex
	accepted int
	open     int
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.accepted++
	l.open++
	l.mu.Unlock()
	return &countingConn{Conn: conn, owner: l}, nil
}

func (l *countingListener) counts() (accepted, open int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accepted, l.open
}

type countingConn struct {
	net.Conn
	owner *countingListener
	once  sync.Once
}

func (c *countingConn) Close() error {
	c.once.Do(func() {
		c.owner.mu.Lock()
		c.owner.open--
		c.owner.mu.Unlock()
	})
	return c.Conn.Close()
}

// startFakeStatsServer 起一台监听 127.0.0.1 随机端口的 StatsService。
func startFakeStatsServer(t *testing.T) (port int, fake *fakeStats, listener *countingListener) {
	t.Helper()

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	listener = &countingListener{Listener: raw}
	fake = &fakeStats{}

	srv := grpc.NewServer(grpc.ForceServerCodec(statsWireCodec{}))
	srv.RegisterService(&statsServiceDesc, fake)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(listener)
	}()
	t.Cleanup(func() {
		srv.Stop()
		<-done
	})
	return raw.Addr().(*net.TCPAddr).Port, fake, listener
}

// waitFor 轮询到条件成立，避免依赖固定的 sleep。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestStatsClientReusesOneConnection 是"每 10 秒一次"这个频率的前提。
//
// 每次拉流量都重新拨号意味着每 10 秒一次 TCP 握手加一次 HTTP/2 初始化，
// 外加一个走完 TIME_WAIT 才消失的 socket。
func TestStatsClientReusesOneConnection(t *testing.T) {
	port, fake, listener := startFakeStatsServer(t)

	client, err := newStatsClient(port)
	if err != nil {
		t.Fatalf("new stats client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	for i := 0; i < 5; i++ {
		traffics, err := client.QueryTraffic(true)
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		if len(traffics) != 2 {
			t.Fatalf("query %d returned %d rows, want an inbound and a user row", i, len(traffics))
		}
	}

	calls, sawReset, sawDeadline, within := fake.snapshot()
	if calls != 5 {
		t.Errorf("server saw %d calls, want 5", calls)
	}
	if !sawReset {
		t.Error("reset=true did not reach the server; counters would never be cleared")
	}
	if !sawDeadline {
		t.Error("the RPC carried no deadline: a wedged core would pin the cron goroutine forever")
	}
	if within <= 0 || within > statsQueryTimeout {
		t.Errorf("the deadline is %v out, want a positive value within %v", within, statsQueryTimeout)
	}
	if accepted, _ := listener.counts(); accepted != 1 {
		t.Errorf("the server accepted %d connections for 5 queries, want 1 reused connection", accepted)
	}
}

// TestStatsClientCloseReleasesTheSocket 覆盖"停掉内核之后连接要还回去"。
func TestStatsClientCloseReleasesTheSocket(t *testing.T) {
	port, _, listener := startFakeStatsServer(t)

	client, err := newStatsClient(port)
	if err != nil {
		t.Fatalf("new stats client: %v", err)
	}
	if _, err := client.QueryTraffic(false); err != nil {
		t.Fatalf("query: %v", err)
	}
	waitFor(t, "the connection to be established", func() bool {
		_, open := listener.counts()
		return open == 1
	})

	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	waitFor(t, "the connection to be released", func() bool {
		_, open := listener.counts()
		return open == 0
	})

	if _, err := client.QueryTraffic(false); err == nil {
		t.Error("QueryTraffic on a closed client must fail instead of dialling again")
	}
	// 重复 Close 是常态：Stop 与 Close 可能都走到这里。
	if err := client.Close(); err != nil {
		t.Errorf("second Close returned %v, want nil", err)
	}
}

// TestProcessCloseReleasesStatsOfADeadCore 是这次修的泄漏本身。
//
// 内核崩溃退出后面板会丢掉旧的 Process 换一个新的。旧实例的 grpc.ClientConn
// 不会随子进程消失：它会一直在后台重连那个已经没人监听的端口。配置永久非法时
// 每个重启周期攒一条，socket 与 goroutine 就这么涨上去。
func TestProcessCloseReleasesStatsOfADeadCore(t *testing.T) {
	port, _, listener := startFakeStatsServer(t)

	p := NewProcess(&Config{})
	client, err := newStatsClient(port)
	if err != nil {
		t.Fatalf("new stats client: %v", err)
	}
	p.mu.Lock()
	p.stats = client
	p.mu.Unlock()

	if _, err := p.GetTraffic(false); err != nil {
		t.Fatalf("get traffic: %v", err)
	}
	waitFor(t, "the connection to be established", func() bool {
		_, open := listener.counts()
		return open == 1
	})

	// 进程从来没跑起来过（模拟已经退出的实例），Close 依然要回收连接。
	if p.IsRunning() {
		t.Fatal("a process that was never started must not report as running")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("close process: %v", err)
	}
	waitFor(t, "the stats connection to be released", func() bool {
		_, open := listener.counts()
		return open == 0
	})

	p.mu.RLock()
	leftover := p.stats
	p.mu.RUnlock()
	if leftover != nil {
		t.Error("the process still holds a stats client after Close")
	}
	if _, err := p.GetTraffic(false); err == nil {
		t.Error("GetTraffic after Close must report that there is no client")
	}
	if err := p.Close(); err != nil {
		t.Errorf("second Close returned %v, want nil", err)
	}
}

// TestQueryTrafficOnClosedClientIsExplicit 保证错误信息说人话，
// 而不是一个来自 grpc 内部的 nil pointer panic。
func TestQueryTrafficOnClosedClientIsExplicit(t *testing.T) {
	port, _, _ := startFakeStatsServer(t)
	client, err := newStatsClient(port)
	if err != nil {
		t.Fatalf("new stats client: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err = client.QueryTraffic(true)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("error = %v, want something that names the closed client", err)
	}
}

func TestNewStatsClientRejectsAnUnsetPort(t *testing.T) {
	for _, port := range []int{0, -1} {
		if _, err := newStatsClient(port); err == nil {
			t.Errorf("port %d was accepted; the panel would dial an arbitrary address", port)
		}
	}
}
