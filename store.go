package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ---------------------------------------------------------------- config

type Person struct {
	ID       string `json:"id"` // "groom" | "bride" (점수 저장 키, 바뀌지 않음)
	Name     string `json:"name"`
	Password string `json:"password"`
}

type Config struct {
	Title     string   `json:"title"`
	People    []Person `json:"people"`
	Secret    string   `json:"secret"`
	Tailscale struct {
		Enabled  bool   `json:"enabled"`
		Hostname string `json:"hostname"`
	} `json:"tailscale"`
}

func (c Config) Configured() bool {
	for _, p := range c.People {
		if p.Name == "" || p.Password == "" {
			return false
		}
	}
	return len(c.People) == 2
}

func (c Config) DisplayTitle() string {
	if c.Title != "" {
		return c.Title
	}
	if c.Configured() {
		return c.People[0].Name + " ♥ " + c.People[1].Name
	}
	return "웨딩 사진 셀렉"
}

func (c Config) Person(id string) *Person {
	for i := range c.People {
		if c.People[i].ID == id {
			return &c.People[i]
		}
	}
	return nil
}

type ConfigStore struct {
	mu   sync.RWMutex
	path string
	c    Config
}

func loadConfig(path string) (*ConfigStore, error) {
	s := &ConfigStore{path: path}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &s.c); err != nil {
			return nil, errors.New("data/config.json 파일이 손상되었습니다: " + err.Error())
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if len(s.c.People) != 2 {
		s.c.People = []Person{{ID: "groom"}, {ID: "bride"}}
	}
	if s.c.Secret == "" {
		buf := make([]byte, 32)
		rand.Read(buf)
		s.c.Secret = hex.EncodeToString(buf)
	}
	if s.c.Tailscale.Hostname == "" {
		s.c.Tailscale.Hostname = "wedding-select"
	}
	return s, s.saveLocked()
}

func (s *ConfigStore) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.c
	c.People = append([]Person(nil), s.c.People...)
	return c
}

func (s *ConfigStore) Update(fn func(c *Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.c)
	return s.saveLocked()
}

func (s *ConfigStore) saveLocked() error {
	b, _ := json.MarshalIndent(s.c, "", "  ")
	return writeAtomic(s.path, b, 0o600)
}

// ---------------------------------------------------------------- scores

type storeData struct {
	Scores map[string]map[string]int `json:"scores"` // photo -> personID -> 1..5
	Finals map[string]string         `json:"finals"` // photo -> personID who picked it
}

type Store struct {
	mu   sync.RWMutex
	path string
	d    storeData
}

func loadStore(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &s.d); err != nil {
			// 손상된 경우 백업 후 새로 시작하지 않고, 사용자에게 알린다.
			return nil, errors.New("data/scores.json 파일이 손상되었습니다: " + err.Error())
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if s.d.Scores == nil {
		s.d.Scores = map[string]map[string]int{}
	}
	if s.d.Finals == nil {
		s.d.Finals = map[string]string{}
	}
	return s, nil
}

func (s *Store) Snapshot() storeData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := storeData{Scores: map[string]map[string]int{}, Finals: map[string]string{}}
	for p, m := range s.d.Scores {
		mm := map[string]int{}
		for k, v := range m {
			mm[k] = v
		}
		out.Scores[p] = mm
	}
	for p, u := range s.d.Finals {
		out.Finals[p] = u
	}
	return out
}

func (s *Store) SetScore(photo, person string, score int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if score == 0 {
		if m := s.d.Scores[photo]; m != nil {
			delete(m, person)
			if len(m) == 0 {
				delete(s.d.Scores, photo)
			}
		}
	} else {
		if s.d.Scores[photo] == nil {
			s.d.Scores[photo] = map[string]int{}
		}
		s.d.Scores[photo][person] = score
	}
	return s.saveLocked()
}

func (s *Store) SetFinal(photo, person string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		s.d.Finals[photo] = person
	} else {
		delete(s.d.Finals, photo)
	}
	return s.saveLocked()
}

// ---- 초기화 / 일괄 변경 (변경 전에 항상 백업을 남긴다)

// backupLocked writes the current data to data/backups/scores-<time>-<reason>.json (keeps the newest 30).
func (s *Store) backupLocked(reason string) {
	dir := filepath.Join(filepath.Dir(s.path), "backups")
	os.MkdirAll(dir, 0o755)
	b, _ := json.Marshal(s.d)
	writeAtomic(filepath.Join(dir, "scores-"+time.Now().Format("20060102-150405.000")+"-"+reason+".json"), b, 0o644)
	if ents, err := os.ReadDir(dir); err == nil && len(ents) > 30 {
		for _, e := range ents[:len(ents)-30] {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// ResetScores removes all scores of one person ("" = everyone).
func (s *Store) ResetScores(person string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backupLocked("reset-scores")
	n := 0
	for p, m := range s.d.Scores {
		if person == "" {
			n += len(m)
			delete(s.d.Scores, p)
			continue
		}
		if _, ok := m[person]; ok {
			delete(m, person)
			n++
			if len(m) == 0 {
				delete(s.d.Scores, p)
			}
		}
	}
	return n, s.saveLocked()
}

// ClearFinals empties the shared 최종셀렉 list.
func (s *Store) ClearFinals() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backupLocked("clear-finals")
	n := len(s.d.Finals)
	s.d.Finals = map[string]string{}
	return n, s.saveLocked()
}

// SetFinals adds photos to finals, or replaces finals with exactly these photos.
func (s *Store) SetFinals(photos []string, person string, replace bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if replace {
		s.backupLocked("replace-finals")
		s.d.Finals = map[string]string{}
	}
	for _, p := range photos {
		if _, ok := s.d.Finals[p]; !ok {
			s.d.Finals[p] = person
		}
	}
	return s.saveLocked()
}

// Backups lists backup files, newest first.
func (s *Store) Backups() []string {
	ents, _ := os.ReadDir(filepath.Join(filepath.Dir(s.path), "backups"))
	var out []string
	for i := len(ents) - 1; i >= 0; i-- {
		out = append(out, ents[i].Name())
	}
	return out
}

// RestoreLatest restores the newest backup (the current state is backed up first, so it can be undone too).
func (s *Store) RestoreLatest() (string, error) {
	list := s.Backups()
	if len(list) == 0 {
		return "", errors.New("되돌릴 백업이 없어요")
	}
	name := list[0]
	b, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), "backups", name))
	if err != nil {
		return "", err
	}
	var d storeData
	if err := json.Unmarshal(b, &d); err != nil {
		return "", err
	}
	if d.Scores == nil {
		d.Scores = map[string]map[string]int{}
	}
	if d.Finals == nil {
		d.Finals = map[string]string{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	os.Remove(filepath.Join(filepath.Dir(s.path), "backups", name))
	s.backupLocked("before-restore")
	s.d = d
	return name, s.saveLocked()
}

var lastBackup time.Time

func (s *Store) saveLocked() error {
	b, _ := json.Marshal(s.d)
	if err := writeAtomic(s.path, b, 0o644); err != nil {
		return err
	}
	// 하루 한 번 백업 사본
	if time.Since(lastBackup) > 24*time.Hour {
		lastBackup = time.Now()
		writeAtomic(filepath.Join(filepath.Dir(s.path), "scores.backup.json"), b, 0o644)
	}
	return nil
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
