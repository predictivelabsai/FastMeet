package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testServer(t *testing.T, mod func(*Config)) *httptest.Server {
	t.Helper()
	cfg := loadConfig()
	cfg.TrustProxy = false
	if mod != nil {
		mod(&cfg)
	}
	ice, stop, err := setupICE(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	ts := httptest.NewServer(newServer(cfg, ice).routes())
	t.Cleanup(ts.Close)
	return ts
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestRoomIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		id := newRoomID()
		if !validRoomID(id) {
			t.Fatalf("invalid id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate %q", id)
		}
		seen[id] = true
	}
	for _, bad := range []string{"", "abcd-efgh-ijk", "ABCD-EFGH-JKMN", "abcd-efgh-jkm0", "abcd-efgh-jkml", "../etc/passwd", "static", "healthz"} {
		if validRoomID(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestLandingHasPoweredByAndCreate(t *testing.T) {
	ts := testServer(t, nil)
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	s := string(b)
	for _, want := range []string{`href="https://socialagent.fun/`, "Powered by Social Agent", `action="/new"`, "Create a room"} {
		if !strings.Contains(s, want) {
			t.Errorf("landing missing %q", want)
		}
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("csp %q", csp)
	}
}

func TestCreateRedirectsAndRateLimits(t *testing.T) {
	ts := testServer(t, func(c *Config) { c.CreatePerMin = 3 })
	cl := noRedirect()
	for i := 0; i < 3; i++ {
		res, err := cl.Post(ts.URL+"/new", "application/x-www-form-urlencoded", nil)
		if err != nil {
			t.Fatal(err)
		}
		loc := res.Header.Get("Location")
		if res.StatusCode != http.StatusSeeOther || !validRoomID(strings.TrimPrefix(loc, "/")) {
			t.Fatalf("create %d: %d %q", i, res.StatusCode, loc)
		}
		page, _ := http.Get(ts.URL + loc)
		pb, _ := io.ReadAll(page.Body)
		if page.StatusCode != 200 || !strings.Contains(string(pb), "socialagent.fun") || !strings.Contains(string(pb), strings.TrimPrefix(loc, "/")) {
			t.Fatalf("room page %d", page.StatusCode)
		}
	}
	res, _ := cl.Post(ts.URL+"/new", "application/x-www-form-urlencoded", nil)
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("4th create got %d, want 429", res.StatusCode)
	}
}

func TestJoinAndBadRoom(t *testing.T) {
	ts := testServer(t, nil)
	cl := noRedirect()
	res, _ := cl.Get(ts.URL + "/join?code=" + "https://meet.socialagent.fun/k7qp-xm3d-9fha")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/k7qp-xm3d-9fha" {
		t.Fatalf("join: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = cl.Get(ts.URL + "/not-a-room")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("bad room: %d", res.StatusCode)
	}
	res, _ = cl.Get(ts.URL + "/K7QP-XM3D-9FHA")
	if res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("upper-case room: %d", res.StatusCode)
	}
}

type wsClient struct {
	c   *websocket.Conn
	ctx context.Context
}

func dial(t *testing.T, ts *httptest.Server, room, name string) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/" + room
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {ts.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	w := &wsClient{c, ctx}
	w.send(t, map[string]any{"type": "join", "name": name})
	return w
}

func (w *wsClient) send(t *testing.T, v any) {
	b, _ := json.Marshal(v)
	if err := w.c.Write(w.ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func (w *wsClient) next(t *testing.T, typ string) msg {
	t.Helper()
	for {
		_, b, err := w.c.Read(w.ctx)
		if err != nil {
			t.Fatalf("waiting for %s: %v", typ, err)
		}
		var m msg
		_ = json.Unmarshal(b, &m)
		if m.Type == typ {
			return m
		}
	}
}

func TestSignalingRelay(t *testing.T) {
	ts := testServer(t, func(c *Config) { c.MaxPeers = 2 })
	room := newRoomID()
	a := dial(t, ts, room, "Ana")
	wa := a.next(t, "welcome")
	if len(wa.Peers) != 0 || len(wa.ICE) == 0 {
		t.Fatalf("welcome a: %+v", wa)
	}
	b := dial(t, ts, room, "Ben")
	wb := b.next(t, "welcome")
	if len(wb.Peers) != 1 || wb.Peers[0].Name != "Ana" {
		t.Fatalf("welcome b: %+v", wb)
	}
	if j := a.next(t, "peer-joined"); j.ID != wb.ID || j.Name != "Ben" {
		t.Fatalf("peer-joined: %+v", j)
	}
	b.send(t, map[string]any{"type": "signal", "to": wa.ID, "data": map[string]any{"description": map[string]string{"type": "offer", "sdp": "x"}}})
	if s := a.next(t, "signal"); s.From != wb.ID || !strings.Contains(string(s.Data), "offer") {
		t.Fatalf("signal: %+v", s)
	}
	a.send(t, map[string]any{"type": "chat", "text": "  hello  "})
	if c := b.next(t, "chat"); c.Text != "hello" || c.Name != "Ana" {
		t.Fatalf("chat: %+v", c)
	}
	// A third person is turned away.
	c := dial(t, ts, room, "Cat")
	if f := c.next(t, "full"); f.Max != 2 {
		t.Fatalf("full: %+v", f)
	}
	b.c.Close(websocket.StatusNormalClosure, "")
	if l := a.next(t, "peer-left"); l.ID != wb.ID {
		t.Fatalf("peer-left: %+v", l)
	}
}

func TestWebsocketRejectsForeignOrigin(t *testing.T) {
	ts := testServer(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/" + newRoomID()
	_, res, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example"}}})
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %v %v", res, err)
	}
}

func TestTURNCredentials(t *testing.T) {
	ice := &ICE{stun: []string{"stun:x"}, turnURLs: []string{"turn:1.2.3.4:3478"}, secret: "s", ttl: time.Hour}
	srv := ice.Servers()
	if len(srv) != 2 || srv[1].Username == "" || srv[1].Credential == "" {
		t.Fatalf("servers: %+v", srv)
	}
}

func TestClientIPUsesProxyHeaders(t *testing.T) {
	s := &server{cfg: Config{TrustProxy: true}}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 9.9.9.9")
	if ip := s.clientIP(r); ip != "9.9.9.9" {
		t.Fatalf("got %s", ip)
	}
	r.Header.Set("X-Real-Ip", "8.8.8.8")
	if ip := s.clientIP(r); ip != "8.8.8.8" {
		t.Fatalf("got %s", ip)
	}
}
