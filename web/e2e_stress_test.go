package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"x-ui/database"
	"x-ui/database/model"
	"x-ui/web/middleware"
)

// stressSession is a browser with an already established CSRF session.
// Sessions are primed before the start gate opens so the tests stress the
// operation under test, not template rendering or token creation.
type stressSession struct {
	client *http.Client
	token  string
}

func prepareStressSessions(t *testing.T, p *panel, count int) []stressSession {
	t.Helper()

	sessions := make([]stressSession, count)
	for i := range sessions {
		b := p.newClient()
		token, _ := b.primeCSRF()
		if token == "" {
			t.Fatalf("session %d received no CSRF token", i)
		}
		sessions[i] = stressSession{client: b.client, token: token}
	}
	return sessions
}

// stressPost deliberately returns errors instead of calling Fatalf so it is
// safe to use from worker goroutines.
func stressPost(client *http.Client, endpoint, token string, form url.Values, headers ...[2]string) (int, apiMsg, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, apiMsg{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set(middleware.HeaderCSRFToken, token)
	for _, header := range headers {
		req.Header.Set(header[0], header[1])
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, apiMsg{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, apiMsg{}, err
	}
	var msg apiMsg
	if err := json.Unmarshal(body, &msg); err != nil {
		return resp.StatusCode, apiMsg{}, fmt.Errorf("decode response %q: %w", body, err)
	}
	return resp.StatusCode, msg, nil
}

type stressResult struct {
	index  int
	status int
	msg    apiMsg
	err    error
}

// Every worker forges a different forwarding chain, but httptest connects
// directly from one untrusted peer. All failures must therefore land in one
// limiter bucket even when RecordFail is hit concurrently.
func TestE2EConcurrentForgedXFFStillLocksThePeer(t *testing.T) {
	p := newPanel(t)
	workers := p.server.loginLimiter.MaxFailures + 3
	sessions := prepareStressSessions(t, p, workers)

	start := make(chan struct{})
	results := make(chan stressResult, workers)
	for i, session := range sessions {
		go func(i int, session stressSession) {
			<-start
			status, msg, err := stressPost(session.client, p.url("login"), session.token, url.Values{
				"username": {p.username},
				"password": {"wrong-password"},
			},
				[2]string{"X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1)},
				[2]string{"X-Real-IP", fmt.Sprintf("203.0.113.%d", i+1)},
			)
			results <- stressResult{index: i, status: status, msg: msg, err: err}
		}(i, session)
	}
	close(start)

	for range workers {
		result := <-results
		if result.err != nil {
			t.Errorf("attempt %d: %v", result.index, result.err)
			continue
		}
		if result.status == http.StatusInternalServerError {
			t.Errorf("attempt %d panicked or failed internally", result.index)
		}
		if result.msg.Success {
			t.Errorf("attempt %d with a wrong password succeeded", result.index)
		}
	}

	fresh := prepareStressSessions(t, p, 1)[0]
	status, msg, err := stressPost(fresh.client, p.url("login"), fresh.token, url.Values{
		"username": {p.username},
		"password": {p.password},
	}, [2]string{"X-Forwarded-For", "192.0.2.250"})
	if err != nil {
		t.Fatal(err)
	}
	if status == http.StatusInternalServerError {
		t.Fatal("the lockout path returned 500")
	}
	if msg.Success {
		t.Fatal("parallel failures were split by forged forwarding headers")
	}
	if !strings.Contains(strings.ToLower(msg.Msg), "lock") && !strings.Contains(msg.Msg, "锁") {
		t.Errorf("message = %q, want the shared peer to be locked", msg.Msg)
	}
}

type inboundBurstCase struct {
	index       int
	path        string
	form        url.Values
	wantSuccess bool
}

func runInboundBurst(p *panel, token string, cases []inboundBurstCase) []stressResult {
	start := make(chan struct{})
	results := make(chan stressResult, len(cases))
	for _, tc := range cases {
		go func(tc inboundBurstCase) {
			<-start
			status, msg, err := stressPost(p.client, p.url(tc.path), token, tc.form)
			results <- stressResult{index: tc.index, status: status, msg: msg, err: err}
		}(tc)
	}
	close(start)

	out := make([]stressResult, len(cases))
	for range cases {
		result := <-results
		out[result.index] = result
	}
	return out
}

func assertInboundBurst(t *testing.T, cases []inboundBurstCase, results []stressResult) {
	t.Helper()

	for _, tc := range cases {
		result := results[tc.index]
		if result.err != nil {
			t.Errorf("request %d: %v", tc.index, result.err)
			continue
		}
		if result.status == http.StatusInternalServerError {
			t.Errorf("request %d returned 500", tc.index)
		}
		if result.msg.Success != tc.wantSuccess {
			t.Errorf("request %d success = %v (%s), want %v",
				tc.index, result.msg.Success, result.msg.Msg, tc.wantSuccess)
		}
	}
}

// This exercises each write phase in a burst. Invalid settings are mixed into
// the create phase, then sent against every persisted row before valid updates.
// No rejected object may appear in SQLite, even transiently after the burst.
func TestE2EBurstInboundCRUDNeverPersistsReservedSettings(t *testing.T) {
	p := newPanel(t)
	p.login()
	token := p.csrfToken()

	const count = 8
	reserved := []string{"type", "tag", "listen", "listen_port"}
	adds := make([]inboundBurstCase, 0, count*2)
	for i := 0; i < count; i++ {
		adds = append(adds, inboundBurstCase{
			index:       len(adds),
			path:        "xui/inbound/add",
			form:        inboundForm(23000+i, "vmess", vmessSettings),
			wantSuccess: true,
		})
		key := reserved[i%len(reserved)]
		adds = append(adds, inboundBurstCase{
			index: len(adds),
			path:  "xui/inbound/add",
			form: inboundForm(24000+i, "vmess",
				`{"users":[],"`+key+`":"forged"}`),
			wantSuccess: false,
		})
	}
	assertInboundBurst(t, adds, runInboundBurst(p, token, adds))

	var inbounds []*model.Inbound
	if err := database.GetDB().Order("id asc").Find(&inbounds).Error; err != nil {
		t.Fatalf("read burst-created inbounds: %v", err)
	}
	if len(inbounds) != count {
		t.Fatalf("persisted %d inbounds after mixed burst, want %d valid rows", len(inbounds), count)
	}
	for _, inbound := range inbounds {
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			t.Fatalf("inbound %d persisted malformed settings: %v", inbound.Id, err)
		}
		for _, key := range reserved {
			if _, found := settings[key]; found {
				t.Errorf("inbound %d persisted reserved key %q", inbound.Id, key)
			}
		}
	}

	invalidUpdates := make([]inboundBurstCase, count)
	for i, inbound := range inbounds {
		key := reserved[i%len(reserved)]
		invalidUpdates[i] = inboundBurstCase{
			index: i,
			path:  "xui/inbound/update/" + strconv.Itoa(inbound.Id),
			form: inboundForm(inbound.Port, "vmess",
				`{"users":[],"`+key+`":"forged"}`),
			wantSuccess: false,
		}
	}
	assertInboundBurst(t, invalidUpdates, runInboundBurst(p, token, invalidUpdates))
	for _, inbound := range inbounds {
		reloaded := &model.Inbound{}
		if err := database.GetDB().First(reloaded, inbound.Id).Error; err != nil {
			t.Fatalf("reload inbound %d: %v", inbound.Id, err)
		}
		if reloaded.Settings != vmessSettings {
			t.Errorf("rejected update changed inbound %d settings to %q", inbound.Id, reloaded.Settings)
		}
	}

	updates := make([]inboundBurstCase, count)
	for i, inbound := range inbounds {
		form := inboundForm(inbound.Port+100, "vmess", vmessSettings)
		form.Set("remark", fmt.Sprintf("updated-%d", i))
		updates[i] = inboundBurstCase{
			index:       i,
			path:        "xui/inbound/update/" + strconv.Itoa(inbound.Id),
			form:        form,
			wantSuccess: true,
		}
	}
	assertInboundBurst(t, updates, runInboundBurst(p, token, updates))

	deletes := make([]inboundBurstCase, count)
	for i, inbound := range inbounds {
		deletes[i] = inboundBurstCase{
			index:       i,
			path:        "xui/inbound/del/" + strconv.Itoa(inbound.Id),
			form:        url.Values{},
			wantSuccess: true,
		}
	}
	assertInboundBurst(t, deletes, runInboundBurst(p, token, deletes))
	if got := p.countInbounds(); got != 0 {
		t.Errorf("%d inbounds survived the delete burst", got)
	}
}

type getResult struct {
	index  int
	status int
	body   string
	err    error
}

func concurrentGET(client *http.Client, endpoints []string, headers func(int, *http.Request)) []getResult {
	start := make(chan struct{})
	results := make(chan getResult, len(endpoints))
	for i, endpoint := range endpoints {
		go func(i int, endpoint string) {
			<-start
			req, err := http.NewRequest(http.MethodGet, endpoint, nil)
			if err != nil {
				results <- getResult{index: i, err: err}
				return
			}
			if headers != nil {
				headers(i, req)
			}
			resp, err := client.Do(req)
			if err != nil {
				results <- getResult{index: i, err: err}
				return
			}
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			results <- getResult{index: i, status: resp.StatusCode, body: string(body), err: readErr}
		}(i, endpoint)
	}
	close(start)

	out := make([]getResult, len(endpoints))
	for range endpoints {
		result := <-results
		out[result.index] = result
	}
	return out
}

func TestE2EParallelSubscriptionLookupsStay200Or404(t *testing.T) {
	p := newPanel(t)
	p.login()
	client := seedSubscription(t, p, 25001)

	const requests = 24
	endpoints := make([]string, requests)
	for i := range endpoints {
		token := client.SubToken
		if i%2 != 0 {
			token = fmt.Sprintf("not-a-token-%02d", i)
		}
		endpoints[i] = p.url("sub/" + token)
	}
	results := concurrentGET(&http.Client{Timeout: 5 * time.Second}, endpoints, nil)
	for i, result := range results {
		if result.err != nil {
			t.Errorf("lookup %d: %v", i, result.err)
			continue
		}
		want := http.StatusOK
		if i%2 != 0 {
			want = http.StatusNotFound
		}
		if result.status != want {
			t.Errorf("lookup %d status = %d, want %d", i, result.status, want)
		}
		if result.status == http.StatusInternalServerError {
			t.Errorf("lookup %d returned 500", i)
		}
	}
}

func TestE2EParallelWrongTwoFactorCodesStillLock(t *testing.T) {
	p := newPanel(t)
	p.login()
	secret, _ := p.enrollTwoFactor()
	p.logout()

	workers := p.server.loginLimiter.MaxFailures + 3
	sessions := prepareStressSessions(t, p, workers)
	start := make(chan struct{})
	results := make(chan stressResult, workers)
	for i, session := range sessions {
		go func(i int, session stressSession) {
			<-start
			status, msg, err := stressPost(session.client, p.url("login"), session.token, url.Values{
				"username":      {p.username},
				"password":      {p.password},
				"twoFactorCode": {"not-a-code"},
			})
			results <- stressResult{index: i, status: status, msg: msg, err: err}
		}(i, session)
	}
	close(start)

	for range workers {
		result := <-results
		if result.err != nil {
			t.Errorf("attempt %d: %v", result.index, result.err)
			continue
		}
		if result.status == http.StatusInternalServerError {
			t.Errorf("attempt %d returned 500 (panic recovered)", result.index)
		}
		if result.msg.Success {
			t.Errorf("attempt %d accepted a wrong two-factor code", result.index)
		}
	}

	p.clearReplayCursor()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate valid code: %v", err)
	}
	fresh := prepareStressSessions(t, p, 1)[0]
	status, msg, err := stressPost(fresh.client, p.url("login"), fresh.token, url.Values{
		"username":      {p.username},
		"password":      {p.password},
		"twoFactorCode": {code},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status == http.StatusInternalServerError {
		t.Fatal("the locked two-factor path returned 500")
	}
	if msg.Success {
		t.Fatal("a valid code bypassed the limiter after parallel wrong-code attempts")
	}
	if !strings.Contains(strings.ToLower(msg.Msg), "lock") && !strings.Contains(msg.Msg, "锁") {
		t.Errorf("message = %q, want a lockout response", msg.Msg)
	}
}

func TestE2EParallelMetricsScrapesPreserveAuthBoundary(t *testing.T) {
	p := newPanel(t)
	const token = "parallel-scrape-token"
	writeSetting(t, "metricsToken", token)

	const requests = 16
	endpoints := make([]string, requests)
	for i := range endpoints {
		endpoints[i] = p.url("/metrics")
	}
	results := concurrentGET(&http.Client{Timeout: 5 * time.Second}, endpoints, func(i int, req *http.Request) {
		if i%2 == 0 {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	})
	for i, result := range results {
		if result.err != nil {
			t.Errorf("scrape %d: %v", i, result.err)
			continue
		}
		want := http.StatusOK
		if i%2 != 0 {
			want = http.StatusUnauthorized
		}
		if result.status != want {
			t.Errorf("scrape %d status = %d, want %d", i, result.status, want)
		}
		if i%2 == 0 && !strings.Contains(result.body, "xui_up 1") {
			t.Errorf("authorized scrape %d has no xui_up metric", i)
		}
		if i%2 != 0 && strings.Contains(result.body, "xui_up") {
			t.Errorf("anonymous scrape %d received metrics", i)
		}
	}
}
