// Wedding Photo Select — 웨딩 사진 셀렉 도우미.
//
// 실행 파일 하나로 동작한다. 실행하면 같은 폴더에 `사진/` 과 `data/` 폴더를 만들고,
// http://127.0.0.1:8765 를 브라우저로 연다. 외부 공유는 내장 Tailscale(Funnel)로 한다.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"
)

//go:embed web
var webFS embed.FS

var version = "dev"

type App struct {
	baseDir   string
	photoDir  string
	dataDir   string
	cfg       *ConfigStore
	store     *Store
	photos    *Library
	ts        *TSManager
	localURL  string
	static    http.Handler
	limiter   *loginLimiter
	startedAt time.Time
}

func main() {
	noBrowser := flag.Bool("no-browser", false, "브라우저를 자동으로 열지 않음")
	port := flag.Int("port", 8765, "로컬 포트 (사용 중이면 다음 포트 사용)")
	dir := flag.String("dir", "", "작업 폴더 (기본: 실행 파일이 있는 폴더)")
	showVersion := flag.Bool("version", false, "버전 출력")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	base := *dir
	if base == "" {
		base = defaultBaseDir()
	}
	app, err := newApp(base)
	if err != nil {
		fatal(err)
	}

	ln, err := listenLocal(*port)
	if err != nil {
		fatal(err)
	}
	app.localURL = "http://" + ln.Addr().String()

	banner(app)

	go app.photos.Run()
	if app.cfg.Get().Tailscale.Enabled {
		app.ts.Start()
	}

	srv := &http.Server{Handler: app.localHandler(), ReadHeaderTimeout: 20 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(err)
		}
	}()

	if !*noBrowser {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(app.localURL + "/admin")
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	fmt.Println("\n종료합니다…")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	app.ts.Stop()
}

func newApp(base string) (*App, error) {
	a := &App{
		baseDir:   base,
		photoDir:  filepath.Join(base, "사진"),
		dataDir:   filepath.Join(base, "data"),
		startedAt: time.Now(),
		limiter:   &loginLimiter{},
	}
	for _, d := range []string{a.photoDir, a.dataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("폴더를 만들 수 없습니다 (%s): %w", d, err)
		}
	}
	var err error
	if a.cfg, err = loadConfig(filepath.Join(a.dataDir, "config.json")); err != nil {
		return nil, err
	}
	if a.store, err = loadStore(filepath.Join(a.dataDir, "scores.json")); err != nil {
		return nil, err
	}
	a.photos = newLibrary(a.photoDir, filepath.Join(a.dataDir, "cache"))
	a.ts = newTSManager(a)
	sub, _ := fs.Sub(webFS, "web")
	a.static = http.FileServer(http.FS(sub))
	return a, nil
}

// defaultBaseDir: 실행 파일이 있는 폴더. `go run` 처럼 임시 폴더에서 실행되면 현재 폴더.
func defaultBaseDir() string {
	exe, err := os.Executable()
	if err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		d := filepath.Dir(exe)
		if !isTempDir(d) {
			return d
		}
	}
	wd, _ := os.Getwd()
	return wd
}

func isTempDir(d string) bool {
	t, _ := filepath.EvalSymlinks(os.TempDir())
	d2, _ := filepath.EvalSymlinks(d)
	return t != "" && len(d2) >= len(t) && d2[:len(t)] == t
}

func listenLocal(port int) (net.Listener, error) {
	var last error
	for p := port; p < port+20; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return ln, nil
		}
		last = err
	}
	return nil, fmt.Errorf("사용 가능한 포트가 없습니다: %w", last)
}

func banner(a *App) {
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println("  💍 Wedding Photo Select", version)
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println("  관리 화면 :", a.localURL+"/admin")
	fmt.Println("  셀렉 화면 :", a.localURL)
	fmt.Println("  사진 폴더 :", a.photoDir)
	fmt.Println()
	fmt.Println("  이 창을 닫으면 프로그램이 종료됩니다.")
	fmt.Println("──────────────────────────────────────────────")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("브라우저를 열 수 없습니다. 직접 여세요: %s", url)
	}
}

func openFolder(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "오류:", err)
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "\n엔터를 누르면 창이 닫힙니다.")
		fmt.Scanln()
	}
	os.Exit(1)
}
