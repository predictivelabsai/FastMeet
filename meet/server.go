package main

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

// Room IDs: three groups of four characters from a 32-symbol alphabet with no
// look-alikes (no l, o, 0, 1): 60 bits of crypto randomness, e.g. "k7qp-xm3d-9fha".
const roomAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

var roomRe = regexp.MustCompile(`^[a-km-np-z2-9]{4}-[a-km-np-z2-9]{4}-[a-km-np-z2-9]{4}$`)

func newRoomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(roomAlphabet[int(c)%len(roomAlphabet)]) // 256 % 32 == 0: unbiased
	}
	return sb.String()
}

func validRoomID(s string) bool { return roomRe.MatchString(s) }

type server struct {
	cfg      Config
	ice      *ICE
	hub      *hub
	tmpl     *template.Template
	create   *ipLimiter
	connects *ipLimiter
}

func newServer(cfg Config, ice *ICE) *server {
	t := template.Must(template.ParseFS(webFS, "web/*.html"))
	return &server{
		cfg:      cfg,
		ice:      ice,
		hub:      newHub(cfg.MaxPeers),
		tmpl:     t,
		create:   newIPLimiter(cfg.CreatePerMin, cfg.CreatePerHr),
		connects: newIPLimiter(30, 300),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": Version})
	})
	mux.HandleFunc("GET /{$}", s.landing)
	mux.HandleFunc("POST /new", s.newRoom)
	mux.HandleFunc("GET /new", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusSeeOther) })
	mux.HandleFunc("GET /join", s.join)
	mux.HandleFunc("GET /ws/{room}", s.ws)
	mux.HandleFunc("GET /{room}", s.room)
	return logRequests(securityHeaders(mux))
}

type pageData struct {
	Version  string
	HomeURL  string
	RoomID   string
	RoomURL  string
	Error    string
	MaxPeers int
}

func (s *server) render(w http.ResponseWriter, name string, status int, d pageData) {
	d.Version, d.HomeURL, d.MaxPeers = Version, s.cfg.HomeURL, s.cfg.MaxPeers
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, name, d); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (s *server) landing(w http.ResponseWriter, r *http.Request) {
	s.render(w, "landing.html", http.StatusOK, pageData{})
}

func (s *server) newRoom(w http.ResponseWriter, r *http.Request) {
	if !s.create.Allow(s.clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		s.render(w, "landing.html", http.StatusTooManyRequests,
			pageData{Error: "You've created a lot of rooms just now. Please wait a minute and try again."})
		return
	}
	http.Redirect(w, r, "/"+newRoomID(), http.StatusSeeOther)
}

// join accepts a bare code or a full meeting link and redirects to the room.
func (s *server) join(w http.ResponseWriter, r *http.Request) {
	code := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("code")))
	code = strings.TrimRight(code, "/")
	if i := strings.LastIndex(code, "/"); i >= 0 {
		code = code[i+1:]
	}
	if !validRoomID(code) {
		s.render(w, "landing.html", http.StatusBadRequest,
			pageData{Error: "That code doesn't look right. It looks like abcd-efgh-jkmn."})
		return
	}
	http.Redirect(w, r, "/"+code, http.StatusSeeOther)
}

func (s *server) roomURL(r *http.Request, id string) string {
	base := s.cfg.PublicURL
	if base == "" {
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			scheme = "http"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/" + id
}

func (s *server) room(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("room"))
	if !validRoomID(id) {
		s.render(w, "landing.html", http.StatusNotFound,
			pageData{Error: "That meeting link doesn't look right. Check the link, or start a new room."})
		return
	}
	if id != r.PathValue("room") {
		http.Redirect(w, r, "/"+id, http.StatusMovedPermanently)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	s.render(w, "room.html", http.StatusOK, pageData{RoomID: id, RoomURL: s.roomURL(r, id)})
}

// clientIP returns the caller's address. Behind Coolify's Traefik the real
// address is in X-Real-Ip (Traefik overwrites it), else the rightmost
// X-Forwarded-For entry, which is the one Traefik appended.
func (s *server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); ip != "" {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		hd.Set("Permissions-Policy", "camera=(self), microphone=(self), display-capture=(self)")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.ServeHTTP(w, r)
	})
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

// Unwrap lets the websocket upgrade reach the underlying Hijacker.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logRequests logs method, route kind and status only: never room IDs, IPs,
// names, chat or anything about the media.
func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		h.ServeHTTP(sw, r)
		route := r.URL.Path
		switch {
		case strings.HasPrefix(route, "/static/"), route == "/healthz":
			return
		case strings.HasPrefix(route, "/ws/"):
			route = "/ws/:room"
		case route != "/" && route != "/new" && route != "/join":
			route = "/:room"
		}
		log.Printf("%s %s %d %s", r.Method, route, sw.code, time.Since(start).Round(time.Millisecond))
	})
}
