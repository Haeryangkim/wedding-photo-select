package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tailscale.com/tailcfg/nodecap"
	"tailscale.com/tsnet"
)

// TSManager runs an embedded Tailscale node and publishes the viewer with Funnel.
//
// States shown in the admin page:
//
//	off         - not started
//	starting    - connecting to Tailscale
//	needs-login - user must log in (AuthURL)
//	needs-funnel- logged in, but HTTPS/Funnel must be enabled for the tailnet (EnableURL)
//	running     - public URL is live (PublicURL)
//	error       - see Error
type TSManager struct {
	app *App

	mu        sync.Mutex
	srv       *tsnet.Server
	ln        net.Listener
	httpSrv   *http.Server
	cancel    context.CancelFunc
	state     string
	authURL   string
	enableURL string
	enableTxt string
	publicURL string
	account   string
	errMsg    string
}

type TSStatus struct {
	State     string `json:"state"`
	AuthURL   string `json:"authURL,omitempty"`
	EnableURL string `json:"enableURL,omitempty"`
	EnableTxt string `json:"enableText,omitempty"`
	PublicURL string `json:"publicURL,omitempty"`
	Account   string `json:"account,omitempty"`
	Error     string `json:"error,omitempty"`
}

func newTSManager(a *App) *TSManager { return &TSManager{app: a, state: "off"} }

func (t *TSManager) Status() TSStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TSStatus{t.state, t.authURL, t.enableURL, t.enableTxt, t.publicURL, t.account, t.errMsg}
}

func (t *TSManager) set(fn func()) {
	t.mu.Lock()
	fn()
	t.mu.Unlock()
}

// Start connects (idempotent).
func (t *TSManager) Start() {
	t.mu.Lock()
	if t.srv != nil {
		t.mu.Unlock()
		return
	}
	cfg := t.app.cfg.Get()
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.srv = &tsnet.Server{
		Dir:      filepath.Join(t.app.dataDir, "tailscale"),
		Hostname: cfg.Tailscale.Hostname,
		Logf:     func(string, ...any) {}, // 내부 로그는 숨김
		UserLogf: func(f string, a ...any) { log.Printf("[tailscale] "+f, a...) },
	}
	t.state, t.errMsg, t.authURL, t.enableURL, t.publicURL = "starting", "", "", "", ""
	srv := t.srv
	t.mu.Unlock()
	go t.run(ctx, srv)
}

// Stop disconnects but keeps the login (so next start needs no login).
func (t *TSManager) Stop() {
	t.mu.Lock()
	srv, hs, cancel := t.srv, t.httpSrv, t.cancel
	t.srv, t.httpSrv, t.ln, t.cancel = nil, nil, nil, nil
	t.state, t.publicURL, t.authURL, t.enableURL = "off", "", "", ""
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if hs != nil {
		hs.Close()
	}
	if srv != nil {
		srv.Close()
	}
}

// Logout removes this device from the Tailscale account.
func (t *TSManager) Logout() error {
	t.mu.Lock()
	srv := t.srv
	t.mu.Unlock()
	if srv != nil {
		if lc, err := srv.LocalClient(); err == nil {
			ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
			lc.Logout(ctx)
			c()
		}
	}
	t.Stop()
	return nil
}

func (t *TSManager) fail(err error) {
	log.Println("[tailscale] 오류:", err)
	t.set(func() { t.state, t.errMsg = "error", err.Error() })
}

func (t *TSManager) run(ctx context.Context, srv *tsnet.Server) {
	if err := srv.Start(); err != nil {
		t.fail(err)
		return
	}
	lc, err := srv.LocalClient()
	if err != nil {
		t.fail(err)
		return
	}
	// 1) 로그인 대기
	for {
		if ctx.Err() != nil {
			return
		}
		st, err := lc.StatusWithoutPeers(ctx)
		if err == nil {
			switch st.BackendState {
			case "NeedsLogin":
				if st.AuthURL != "" {
					t.set(func() { t.state, t.authURL = "needs-login", st.AuthURL })
				}
			case "NeedsMachineAuth":
				t.set(func() {
					t.state, t.errMsg = "error", "Tailscale 관리 화면에서 이 기기를 승인해 주세요."
				})
			case "Running":
				if st.Self != nil {
					acct := ""
					if u, ok := st.User[st.Self.UserID]; ok {
						acct = u.LoginName
					}
					t.set(func() { t.authURL, t.account = "", acct })
				}
				goto loggedIn
			}
		}
		sleepCtx(ctx, time.Second)
	}
loggedIn:
	// 2) HTTPS + Funnel 권한 확인 (없으면 켜는 링크 안내)
	for {
		if ctx.Err() != nil {
			return
		}
		st, err := lc.StatusWithoutPeers(ctx)
		if err == nil && st.Self != nil && st.Self.HasCap(nodecap.HTTPS) && st.Self.HasCap(nodecap.Funnel) && len(st.CertDomains) > 0 {
			break
		}
		if q, err := lc.QueryFeature(ctx, "funnel"); err == nil && !q.Complete {
			t.set(func() { t.state, t.enableURL, t.enableTxt = "needs-funnel", q.URL, q.Text })
		}
		sleepCtx(ctx, 3*time.Second)
	}
	t.set(func() { t.enableURL, t.enableTxt = "", "" })

	// 3) Funnel 시작
	ln, err := srv.ListenFunnel("tcp", ":443", tsnet.FunnelOnly())
	if err != nil {
		t.fail(fmt.Errorf("Funnel을 시작할 수 없습니다: %w", err))
		return
	}
	domain := ""
	if d := srv.CertDomains(); len(d) > 0 {
		domain = d[0]
	}
	hs := &http.Server{Handler: t.app.publicHandler(), ReadHeaderTimeout: 20 * time.Second,
		ErrorLog: log.New(discard{}, "", 0)}
	t.set(func() {
		t.ln, t.httpSrv = ln, hs
		t.state, t.publicURL = "running", "https://"+domain
	})
	log.Println("[tailscale] 외부 공유 주소:", "https://"+domain)
	go warmCert(domain)
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
		t.fail(err)
	}
}

// warmCert makes the first visitor not wait for certificate issuance.
func warmCert(domain string) {
	time.Sleep(2 * time.Second)
	c := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{}}}
	if r, err := c.Get("https://" + domain + "/healthz"); err == nil {
		r.Body.Close()
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// sanitizeHostname: Tailscale 기기 이름 규칙 (영문 소문자/숫자/하이픈).
func sanitizeHostname(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == ' ' || r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = out[:40]
	}
	if out == "" {
		out = "wedding-select"
	}
	return out
}
