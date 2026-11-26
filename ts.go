package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
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
	ready     bool // 외부에서 실제로 접속되는지 확인됨 (DNS 전파 + 인증서 발급 완료)
	account   string
	errMsg    string
}

type TSStatus struct {
	State     string `json:"state"`
	AuthURL   string `json:"authURL,omitempty"`
	EnableURL string `json:"enableURL,omitempty"`
	EnableTxt string `json:"enableText,omitempty"`
	PublicURL string `json:"publicURL,omitempty"`
	Ready     bool   `json:"ready"`
	Account   string `json:"account,omitempty"`
	Error     string `json:"error,omitempty"`
}

func newTSManager(a *App) *TSManager { return &TSManager{app: a, state: "off"} }

func (t *TSManager) Status() TSStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TSStatus{State: t.state, AuthURL: t.authURL, EnableURL: t.enableURL, EnableTxt: t.enableTxt,
		PublicURL: t.publicURL, Ready: t.ready, Account: t.account, Error: t.errMsg}
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
		Logf:     tsDebugLogf(), // 내부 로그는 WS_DEBUG=1 일 때만
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
	t.state, t.publicURL, t.authURL, t.enableURL, t.ready = "off", "", "", "", false
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

// HasLogin reports whether a saved Tailscale login exists on disk.
func (t *TSManager) HasLogin() bool {
	_, err := os.Stat(filepath.Join(t.app.dataDir, "tailscale", "tailscaled.state"))
	return err == nil
}

// Logout removes this device from the Tailscale account and deletes the saved login.
func (t *TSManager) Logout() error {
	t.mu.Lock()
	srv := t.srv
	t.mu.Unlock()
	if srv == nil && t.HasLogin() { // 꺼져 있으면 잠깐 켜서 로그아웃
		srv = &tsnet.Server{Dir: filepath.Join(t.app.dataDir, "tailscale"),
			Hostname: t.app.cfg.Get().Tailscale.Hostname, Logf: func(string, ...any) {}}
		if err := srv.Start(); err == nil {
			defer srv.Close()
		}
	}
	if srv != nil {
		if lc, err := srv.LocalClient(); err == nil {
			ctx, c := context.WithTimeout(context.Background(), 15*time.Second)
			if err := lc.Logout(ctx); err != nil {
				log.Println("[tailscale] 로그아웃 실패:", err)
			}
			c()
		}
	}
	t.Stop()
	os.RemoveAll(filepath.Join(t.app.dataDir, "tailscale"))
	t.set(func() { t.account = "" })
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
		t.state, t.publicURL, t.ready = "running", "https://"+domain, false
	})
	log.Println("[tailscale] 외부 공유 주소:", "https://"+domain)
	go t.waitReachable(ctx, domain)
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
		t.fail(err)
	}
}

// waitReachable checks the public URL from the outside until it answers, so the
// admin page only shows the address once it really works. New Funnel names take
// a minute or two to appear in DNS, and the first HTTPS request triggers
// certificate issuance. Lookups go to public resolvers directly so that an early
// "not found" answer is never cached by this computer's own DNS.
func (t *TSManager) waitReachable(ctx context.Context, domain string) {
	resolvers := []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}
	start := time.Now()
	for i := 0; ctx.Err() == nil; i++ {
		srvAddr := resolvers[i%len(resolvers)]
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", srvAddr)
		}}
		lctx, c := context.WithTimeout(ctx, 6*time.Second)
		ips, err := r.LookupHost(lctx, domain)
		c()
		if err == nil && len(ips) > 0 {
			ip := ips[0]
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{ServerName: domain},
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					_, port, _ := net.SplitHostPort(addr)
					return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, port))
				},
			}
			cl := &http.Client{Timeout: 60 * time.Second, Transport: tr}
			resp, err := cl.Get("https://" + domain + "/healthz")
			tr.CloseIdleConnections()
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					log.Printf("[tailscale] 외부 접속 확인 완료 (%s)", time.Since(start).Round(time.Second))
					t.set(func() {
						if t.publicURL == "https://"+domain {
							t.ready = true
						}
					})
					return
				}
			}
		}
		sleepCtx(ctx, 5*time.Second)
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

func tsDebugLogf() func(string, ...any) {
	if os.Getenv("WS_DEBUG") != "" {
		return func(f string, a ...any) { log.Printf("[ts-debug] "+f, a...) }
	}
	return func(string, ...any) {}
}
