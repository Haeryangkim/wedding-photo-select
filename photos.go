package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/image/draw"
)

type Photo struct {
	Name   string `json:"name"`
	W      int    `json:"w"`
	H      int    `json:"h"`
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"`
	Orient int    `json:"orient"`
}

var sizes = map[string]struct{ edge, quality int }{
	"view":  {2400, 85},
	"thumb": {480, 80},
}

type Library struct {
	dir, cache string

	mu     sync.RWMutex
	list   []Photo
	byName map[string]Photo

	genMu    sync.Mutex
	genLocks map[string]*sync.Mutex

	cached  atomic.Int64
	kick    chan struct{}
	working atomic.Bool
}

func newLibrary(dir, cache string) *Library {
	l := &Library{dir: dir, cache: cache, byName: map[string]Photo{},
		genLocks: map[string]*sync.Mutex{}, kick: make(chan struct{}, 1)}
	os.MkdirAll(filepath.Join(cache, "view"), 0o755)
	os.MkdirAll(filepath.Join(cache, "thumb"), 0o755)
	l.loadIndex()
	l.Rescan()
	return l
}

func isImage(name string) bool {
	n := strings.ToLower(name)
	return !strings.HasPrefix(n, ".") &&
		(strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg") || strings.HasSuffix(n, ".png"))
}

func (l *Library) indexPath() string { return filepath.Join(l.cache, "index.json") }

func (l *Library) loadIndex() {
	b, err := os.ReadFile(l.indexPath())
	if err != nil {
		return
	}
	var list []Photo
	if json.Unmarshal(b, &list) == nil {
		for _, p := range list {
			l.byName[p.Name] = p
		}
	}
}

// Rescan reads the photo folder; only new/changed files are opened.
func (l *Library) Rescan() int {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		log.Println("사진 폴더를 읽을 수 없습니다:", err)
		return 0
	}
	l.mu.RLock()
	old := l.byName
	l.mu.RUnlock()

	var list []Photo
	for _, e := range entries {
		if e.IsDir() || !isImage(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if p, ok := old[e.Name()]; ok && p.Size == info.Size() && p.MTime == info.ModTime().Unix() {
			list = append(list, p)
			continue
		}
		p, err := probe(filepath.Join(l.dir, e.Name()))
		if err != nil {
			log.Printf("건너뜀 (읽을 수 없는 이미지): %s: %v", e.Name(), err)
			continue
		}
		p.Name, p.Size, p.MTime = e.Name(), info.Size(), info.ModTime().Unix()
		// 같은 이름의 파일이 바뀌었으면 캐시 삭제
		if _, ok := old[e.Name()]; ok {
			for k := range sizes {
				os.Remove(l.cachePath(k, e.Name()))
			}
		}
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return naturalLess(list[i].Name, list[j].Name) })

	by := make(map[string]Photo, len(list))
	for _, p := range list {
		by[p.Name] = p
	}
	l.mu.Lock()
	l.list, l.byName = list, by
	l.mu.Unlock()
	if b, err := json.Marshal(list); err == nil {
		writeAtomic(l.indexPath(), b, 0o644)
	}
	l.countCached()
	select {
	case l.kick <- struct{}{}:
	default:
	}
	return len(list)
}

func (l *Library) List() []Photo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.list
}

func (l *Library) Get(name string) (Photo, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p, ok := l.byName[name]
	return p, ok
}

func (l *Library) Path(name string) string { return filepath.Join(l.dir, name) }

func (l *Library) cachePath(kind, name string) string {
	return filepath.Join(l.cache, kind, strings.TrimSuffix(name, filepath.Ext(name))+".jpg")
}

func (l *Library) hasCache(name string) bool {
	for k := range sizes {
		if _, err := os.Stat(l.cachePath(k, name)); err != nil {
			return false
		}
	}
	return true
}

func (l *Library) countCached() {
	var n int64
	for _, p := range l.List() {
		if l.hasCache(p.Name) {
			n++
		}
	}
	l.cached.Store(n)
}

// Progress returns (cached, total).
func (l *Library) Progress() (int, int) { return int(l.cached.Load()), len(l.List()) }

// Run warms the cache in the background and rescans every minute.
func (l *Library) Run() {
	tick := time.NewTicker(60 * time.Second)
	for {
		l.warm()
		select {
		case <-l.kick:
		case <-tick.C:
			l.Rescan()
		}
	}
}

func (l *Library) warm() {
	var todo []string
	for _, p := range l.List() {
		if !l.hasCache(p.Name) {
			todo = append(todo, p.Name)
		}
	}
	if len(todo) == 0 {
		return
	}
	l.working.Store(true)
	defer l.working.Store(false)
	workers := runtime.NumCPU() / 2
	if workers < 2 {
		workers = 2
	}
	if workers > 8 {
		workers = 8
	}
	log.Printf("미리보기 이미지 생성 중: %d장 (처음 한 번만)", len(todo))
	start := time.Now()
	ch := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range ch {
				if _, err := l.Derivative("view", n); err == nil {
					l.cached.Add(1)
				}
			}
		}()
	}
	for _, n := range todo {
		ch <- n
	}
	close(ch)
	wg.Wait()
	log.Printf("미리보기 생성 완료 (%s)", time.Since(start).Round(time.Second))
}

// Derivative returns the path of a resized copy, generating it if needed.
// Generating "view" also generates "thumb" from it.
func (l *Library) Derivative(kind, name string) (string, error) {
	dst := l.cachePath(kind, name)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	l.genMu.Lock()
	m := l.genLocks[name]
	if m == nil {
		m = &sync.Mutex{}
		l.genLocks[name] = m
	}
	l.genMu.Unlock()
	m.Lock()
	defer m.Unlock()
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	p, _ := l.Get(name)
	img, err := decodeFile(l.Path(name))
	if err != nil {
		log.Printf("이미지 처리 실패 %s: %v", name, err)
		img = placeholder()
	}
	view := orient(resize(img, sizes["view"].edge), p.Orient)
	if err := saveJPEG(l.cachePath("view", name), view, sizes["view"].quality); err != nil {
		return "", err
	}
	thumb := resize(view, sizes["thumb"].edge)
	if err := saveJPEG(l.cachePath("thumb", name), thumb, sizes["thumb"].quality); err != nil {
		return "", err
	}
	return dst, nil
}

func decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(bufio.NewReaderSize(f, 1<<20))
	return img, err
}

func placeholder() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 300, 200))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{60, 60, 60, 255}}, image.Point{}, draw.Src)
	return img
}

func resize(src image.Image, edge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= edge && h <= edge {
		return src
	}
	var nw, nh int
	if w >= h {
		nw, nh = edge, h*edge/w
	} else {
		nw, nh = w*edge/h, edge
	}
	// 큰 축소는 먼저 빠른 근사로 2배 크기까지 줄인 뒤 고품질로 마무리.
	if w > nw*3 {
		mid := image.NewRGBA(image.Rect(0, 0, nw*2, nh*2))
		draw.ApproxBiLinear.Scale(mid, mid.Bounds(), src, b, draw.Src, nil)
		src, b = mid, mid.Bounds()
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

func saveJPEG(path string, img image.Image, q int) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	if err := jpeg.Encode(w, img, &jpeg.Options{Quality: q}); err != nil {
		f.Close()
		return err
	}
	w.Flush()
	f.Close()
	return os.Rename(tmp, path)
}

// orient applies an EXIF orientation (1..8) to img.
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// probe reads image dimensions and EXIF orientation without decoding pixels.
func probe(path string) (Photo, error) {
	f, err := os.Open(path)
	if err != nil {
		return Photo{}, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(bufio.NewReader(f))
	if err != nil {
		return Photo{}, err
	}
	f.Seek(0, io.SeekStart)
	o := exifOrientation(f)
	p := Photo{W: cfg.Width, H: cfg.Height, Orient: o}
	if o >= 5 {
		p.W, p.H = p.H, p.W
	}
	return p, nil
}

func exifOrientation(r io.Reader) int {
	br := bufio.NewReader(r)
	var soi [2]byte
	if _, err := io.ReadFull(br, soi[:]); err != nil || soi != [2]byte{0xFF, 0xD8} {
		return 1
	}
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(br, hdr[:]); err != nil || hdr[0] != 0xFF {
			return 1
		}
		marker := hdr[1]
		n := int(binary.BigEndian.Uint16(hdr[2:])) - 2
		if marker == 0xDA || n < 0 {
			return 1
		}
		if marker != 0xE1 {
			if _, err := br.Discard(n); err != nil {
				return 1
			}
			continue
		}
		seg := make([]byte, n)
		if _, err := io.ReadFull(br, seg); err != nil {
			return 1
		}
		if len(seg) < 14 || string(seg[:6]) != "Exif\x00\x00" {
			continue
		}
		t := seg[6:]
		var bo binary.ByteOrder
		switch string(t[:2]) {
		case "II":
			bo = binary.LittleEndian
		case "MM":
			bo = binary.BigEndian
		default:
			return 1
		}
		off := int(bo.Uint32(t[4:8]))
		if off+2 > len(t) {
			return 1
		}
		cnt := int(bo.Uint16(t[off:]))
		for i := 0; i < cnt; i++ {
			e := off + 2 + i*12
			if e+12 > len(t) {
				return 1
			}
			if bo.Uint16(t[e:]) == 0x0112 {
				v := int(bo.Uint16(t[e+8:]))
				if v >= 1 && v <= 8 {
					return v
				}
				return 1
			}
		}
		return 1
	}
}

// naturalLess sorts "IMG_2" before "IMG_10".
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		ca, cb := a[0], b[0]
		if isDigit(ca) && isDigit(cb) {
			na, ra := splitNum(a)
			nb, rb := splitNum(b)
			ta, tb := strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if len(ta) != len(tb) {
				return len(ta) < len(tb)
			}
			if ta != tb {
				return ta < tb
			}
			a, b = ra, rb
			continue
		}
		if ca != cb {
			return ca < cb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func splitNum(s string) (string, string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}
