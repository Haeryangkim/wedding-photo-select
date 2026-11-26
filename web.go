package main

import (
	"archive/zip"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionDays = 30

type handler struct {
	app    *App
	public bool // served over Tailscale Funnel: no admin, rate-limited login
}

func (a *App) localHandler() http.Handler  { return &handler{app: a} }
func (a *App) publicHandler() http.Handler { return &handler{app: a, public: true} }

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a := h.app
	if !h.public && !localHost(r.Host) { // DNS rebinding 방지
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	if r.Method == http.MethodPost && !jsonOrUpload(r) { // CSRF 방지: 단순 요청 차단
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p := r.URL.Path
	switch {
	case p == "/healthz":
		io.WriteString(w, "ok")
	case p == "/" || p == "/index.html":
		if !h.public && !a.cfg.Get().Configured() {
			http.Redirect(w, r, "/admin", http.StatusFound)
			return
		}
		h.serveStatic(w, r, "index.html")
	case strings.HasPrefix(p, "/static/"):
		h.serveStatic(w, r, strings.TrimPrefix(p, "/static/"))
	case p == "/admin" && !h.public:
		h.serveStatic(w, r, "admin.html")
	case p == "/admin/ts" && !h.public: // Tailscale 로그인 팝업 (스스로 상태를 따라감)
		h.serveStatic(w, r, "ts-popup.html")
	case strings.HasPrefix(p, "/api/admin/") && !h.public:
		h.admin(w, r, strings.TrimPrefix(p, "/api/admin/"))
	case p == "/api/me":
		h.me(w, r)
	case p == "/api/login" && r.Method == http.MethodPost:
		h.login(w, r)
	case p == "/api/logout" && r.Method == http.MethodPost:
		http.SetCookie(w, &http.Cookie{Name: "ws", Value: "", Path: "/", MaxAge: -1})
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		user := h.user(r)
		if user == "" {
			writeJSON(w, 401, map[string]any{"error": "로그인이 필요합니다"})
			return
		}
		h.authed(w, r, user)
	}
}

func localHost(host string) bool {
	hn, _, err := net.SplitHostPort(host)
	if err != nil {
		hn = host
	}
	return hn == "127.0.0.1" || hn == "localhost" || hn == "[::1]" || hn == "::1"
}

func jsonOrUpload(r *http.Request) bool {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return ct == "application/json" || r.Header.Get("X-WS") == "1"
}

func (h *handler) serveStatic(w http.ResponseWriter, r *http.Request, name string) {
	b, err := webFS.ReadFile("web/" + name)
	if err != nil || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

// ---------------------------------------------------------------- auth

func (h *handler) sign(id string, exp int64) string {
	c := h.app.cfg.Get()
	pw := ""
	if p := c.Person(id); p != nil {
		pw = p.Password // 비밀번호를 바꾸면 기존 로그인은 풀린다
	}
	m := hmac.New(sha256.New, []byte(c.Secret))
	fmt.Fprintf(m, "%s|%d|%s", id, exp, pw)
	return hex.EncodeToString(m.Sum(nil))
}

func (h *handler) user(r *http.Request) string {
	ck, err := r.Cookie("ws")
	if err != nil {
		return ""
	}
	parts := strings.Split(ck.Value, ".")
	if len(parts) != 3 {
		return ""
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || exp < time.Now().Unix() || h.app.cfg.Get().Person(parts[0]) == nil {
		return ""
	}
	if !hmac.Equal([]byte(parts[2]), []byte(h.sign(parts[0], exp))) {
		return ""
	}
	return parts[0]
}

type loginLimiter struct {
	mu    sync.Mutex
	fails []time.Time
}

func (l *loginLimiter) allowed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-30 * time.Minute)
	k := l.fails[:0]
	for _, t := range l.fails {
		if t.After(cut) {
			k = append(k, t)
		}
	}
	l.fails = k
	return len(l.fails) < 10
}

func (l *loginLimiter) fail() {
	l.mu.Lock()
	l.fails = append(l.fails, time.Now())
	l.mu.Unlock()
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	if h.public && !h.app.limiter.allowed() {
		writeJSON(w, 429, map[string]any{"error": "비밀번호를 너무 많이 틀렸어요. 30분 뒤 다시 시도하세요."})
		return
	}
	var body struct{ Password string }
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "잘못된 요청"})
		return
	}
	c := h.app.cfg.Get()
	if !c.Configured() {
		writeJSON(w, 403, map[string]any{"error": "아직 설정이 끝나지 않았어요"})
		return
	}
	pw := strings.TrimSpace(body.Password)
	who := ""
	for _, p := range c.People {
		if subtle.ConstantTimeCompare([]byte(pw), []byte(p.Password)) == 1 {
			who = p.ID
		}
	}
	if who == "" {
		if h.public {
			h.app.limiter.fail()
		}
		time.Sleep(700 * time.Millisecond)
		writeJSON(w, 403, map[string]any{"error": "비밀번호가 틀렸습니다"})
		return
	}
	exp := time.Now().Add(sessionDays * 24 * time.Hour).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: "ws", Value: who + "." + strconv.FormatInt(exp, 10) + "." + h.sign(who, exp),
		Path: "/", MaxAge: sessionDays * 86400, HttpOnly: true, Secure: h.public,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, 200, map[string]any{"user": who})
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	c := h.app.cfg.Get()
	writeJSON(w, 200, map[string]any{
		"user": nullable(h.user(r)), "title": c.DisplayTitle(), "configured": c.Configured(),
		"people": peopleOut(c), "local": !h.public,
	})
}

func peopleOut(c Config) []map[string]string {
	var out []map[string]string
	for _, p := range c.People {
		out = append(out, map[string]string{"id": p.ID, "name": p.Name})
	}
	return out
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---------------------------------------------------------------- viewer API

func (h *handler) authed(w http.ResponseWriter, r *http.Request, user string) {
	a := h.app
	p := r.URL.Path
	switch {
	case p == "/api/photos":
		list := a.photos.List()
		out := make([]map[string]any, len(list))
		for i, ph := range list {
			out[i] = map[string]any{"name": ph.Name, "w": ph.W, "h": ph.H}
		}
		s := a.store.Snapshot()
		writeJSON(w, 200, map[string]any{"photos": out, "scores": s.Scores, "finals": s.Finals})
	case p == "/api/state":
		s := a.store.Snapshot()
		writeJSON(w, 200, map[string]any{"scores": s.Scores, "finals": s.Finals, "count": len(a.photos.List())})
	case p == "/api/score" && r.Method == http.MethodPost:
		var b struct {
			Photo string
			Score int
		}
		if readJSON(r, &b) != nil || b.Score < 0 || b.Score > 5 {
			writeJSON(w, 400, map[string]any{"error": "잘못된 요청"})
			return
		}
		if _, ok := a.photos.Get(b.Photo); !ok {
			writeJSON(w, 404, map[string]any{"error": "사진 없음"})
			return
		}
		// 점수는 로그인한 본인 것만 바뀐다 (상대 점수는 변경 불가)
		if err := a.store.SetScore(b.Photo, user, b.Score); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		s := a.store.Snapshot()
		writeJSON(w, 200, map[string]any{"scores": s.Scores, "finals": s.Finals})
	case p == "/api/final" && r.Method == http.MethodPost:
		var b struct {
			Photo string
			On    bool
		}
		if readJSON(r, &b) != nil {
			writeJSON(w, 400, map[string]any{"error": "잘못된 요청"})
			return
		}
		if _, ok := a.photos.Get(b.Photo); !ok {
			writeJSON(w, 404, map[string]any{"error": "사진 없음"})
			return
		}
		if err := a.store.SetFinal(b.Photo, user, b.On); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		s := a.store.Snapshot()
		writeJSON(w, 200, map[string]any{"scores": s.Scores, "finals": s.Finals})
	case p == "/api/download-finals":
		h.downloadFinals(w, r)
	case strings.HasPrefix(p, "/img/"):
		h.image(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *handler) image(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/img/"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	kind, name := parts[0], parts[1]
	ph, ok := h.app.photos.Get(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var path string
	switch kind {
	case "orig":
		path = h.app.photos.Path(ph.Name)
		if r.URL.Query().Get("dl") == "1" {
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(ph.Name))
		}
	case "view", "thumb":
		var err error
		if path, err = h.app.photos.Derivative(kind, ph.Name); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=604800")
	http.ServeFile(w, r, path)
}

func (h *handler) downloadFinals(w http.ResponseWriter, r *http.Request) {
	a := h.app
	c := a.cfg.Get()
	s := a.store.Snapshot()
	var names []string
	for _, p := range a.photos.List() {
		if _, ok := s.Finals[p.Name]; ok {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		writeJSON(w, 400, map[string]any{"error": "최종셀렉된 사진이 없습니다"})
		return
	}
	nameOf := func(id string) string {
		if p := c.Person(id); p != nil {
			return p.Name
		}
		return id
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s 최종셀렉 %d장 (%s)\r\n\r\n파일명\t%s\t%s\t고른 사람\r\n",
		c.DisplayTitle(), len(names), time.Now().Format("2006-01-02 15:04"), c.People[0].Name, c.People[1].Name)
	for _, n := range names {
		sc := s.Scores[n]
		g := func(id string) string {
			if v := sc[id]; v > 0 {
				return strconv.Itoa(v)
			}
			return "-"
		}
		fmt.Fprintf(&sb, "%s\t%s\t%s\t%s\r\n", n, g(c.People[0].ID), g(c.People[1].ID), nameOf(s.Finals[n]))
	}
	fname := fmt.Sprintf("최종셀렉_%d장_%s.zip", len(names), time.Now().Format("20060102"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(fname))
	zw := zip.NewWriter(w)
	defer zw.Close()
	if f, err := zw.CreateHeader(&zip.FileHeader{Name: "최종셀렉_목록.txt", Method: zip.Deflate, Modified: time.Now()}); err == nil {
		io.WriteString(f, sb.String())
	}
	for _, n := range names {
		src, err := os.Open(a.photos.Path(n))
		if err != nil {
			continue
		}
		info, _ := src.Stat()
		hdr := &zip.FileHeader{Name: n, Method: zip.Store}
		if info != nil {
			hdr.Modified = info.ModTime()
		}
		if f, err := zw.CreateHeader(hdr); err == nil {
			io.Copy(f, src)
		}
		src.Close()
	}
}

// ---------------------------------------------------------------- admin API (local only)

func (h *handler) admin(w http.ResponseWriter, r *http.Request, action string) {
	a := h.app
	if r.Method != http.MethodPost && action != "status" {
		http.Error(w, "method not allowed", 405)
		return
	}
	switch action {
	case "status":
		c := a.cfg.Get()
		done, total := a.photos.Progress()
		if done > total {
			done = total
		}
		s := a.store.Snapshot()
		writeJSON(w, 200, map[string]any{
			"title": c.Title, "displayTitle": c.DisplayTitle(), "people": c.People,
			"configured": c.Configured(), "hostname": c.Tailscale.Hostname,
			"photos": total, "cached": done, "photoDir": a.photoDir, "baseDir": a.baseDir, "localURL": a.localURL,
			"finals": len(s.Finals), "scored": len(s.Scores),
			"tailscale": a.ts.Status(), "tsLogin": a.ts.HasLogin(), "version": version,
		})
	case "config":
		var b struct {
			Title    string
			People   []Person
			Hostname string
		}
		if err := readJSON(r, &b); err != nil || len(b.People) != 2 {
			writeJSON(w, 400, map[string]any{"error": "잘못된 요청"})
			return
		}
		for i := range b.People {
			b.People[i].Name = strings.TrimSpace(b.People[i].Name)
			b.People[i].Password = strings.TrimSpace(b.People[i].Password)
			if b.People[i].Name == "" {
				writeJSON(w, 400, map[string]any{"error": "이름을 입력해 주세요"})
				return
			}
			if len([]rune(b.People[i].Password)) < 4 {
				writeJSON(w, 400, map[string]any{"error": "비밀번호는 4자 이상으로 해주세요"})
				return
			}
		}
		if b.People[0].Password == b.People[1].Password {
			writeJSON(w, 400, map[string]any{"error": "두 사람의 비밀번호는 서로 달라야 해요 (비밀번호로 누구인지 구분합니다)"})
			return
		}
		host := sanitizeHostname(b.Hostname)
		hostChanged := false
		a.cfg.Update(func(c *Config) {
			c.Title = strings.TrimSpace(b.Title)
			c.People[0].Name, c.People[0].Password = b.People[0].Name, b.People[0].Password
			c.People[1].Name, c.People[1].Password = b.People[1].Name, b.People[1].Password
			hostChanged = c.Tailscale.Hostname != host
			c.Tailscale.Hostname = host
		})
		if hostChanged && a.ts.Status().State != "off" {
			a.ts.Stop()
			a.ts.Start()
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "upload":
		h.upload(w, r)
	case "open-base":
		if err := openFolder(a.baseDir); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "rescan":
		n := a.photos.Rescan()
		writeJSON(w, 200, map[string]any{"photos": n})
	case "open-folder":
		if err := openFolder(a.photoDir); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "ts-start":
		a.cfg.Update(func(c *Config) { c.Tailscale.Enabled = true })
		a.ts.Start()
		writeJSON(w, 200, a.ts.Status())
	case "ts-stop":
		a.cfg.Update(func(c *Config) { c.Tailscale.Enabled = false })
		a.ts.Stop()
		writeJSON(w, 200, a.ts.Status())
	case "ts-retry":
		a.ts.Stop()
		a.ts.Start()
		writeJSON(w, 200, a.ts.Status())
	case "ts-logout":
		a.cfg.Update(func(c *Config) { c.Tailscale.Enabled = false })
		a.ts.Logout()
		writeJSON(w, 200, a.ts.Status())
	case "quit":
		writeJSON(w, 200, map[string]any{"ok": true})
		go func() {
			time.Sleep(300 * time.Millisecond)
			a.ts.Stop()
			log.Println("관리 화면에서 종료했습니다.")
			os.Exit(0)
		}()
	default:
		http.NotFound(w, r)
	}
}

// upload: multipart/form-data with one or more "file" parts, streamed to disk.
func (h *handler) upload(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "잘못된 업로드"})
		return
	}
	saved, skipped := 0, 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		name := filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if part.FormName() != "file" || name == "." || name == "/" || !isImage(name) {
			skipped++
			part.Close()
			continue
		}
		dst := filepath.Join(h.app.photoDir, name)
		f, err := os.CreateTemp(h.app.photoDir, ".upload-*")
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		tmp := f.Name()
		_, err = io.Copy(f, part)
		f.Close()
		part.Close()
		if err != nil {
			os.Remove(tmp)
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		if err := os.Rename(tmp, dst); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		saved++
	}
	writeJSON(w, 200, map[string]any{"saved": saved, "skipped": skipped})
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Println(err)
	}
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v)
}
