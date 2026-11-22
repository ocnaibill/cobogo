// Package backup é o backup do banco interno do Cobogó (DEC-47, parte A): o
// nome e a retenção dos arquivos, o estado que o painel mostra e a execução de
// um dump consistente. O dump leva os segredos **cifrados**; a chave não entra.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// Prefix e Suffix formam o nome de um backup: cobogo-core-20261122-030000[-rótulo].dump
	Prefix = "cobogo-core-"
	Suffix = ".dump"
	// StatusFile é o arquivo de estado que o serviço escreve e a API lê.
	StatusFile = "status.json"
	// KeepLabeled é quantos backups com rótulo (como pre-<commit>) se guardam.
	KeepLabeled = 10
	// StaleAfter é a idade do último backup bem-sucedido a partir da qual o painel avisa.
	StaleAfter = 36 * time.Hour
)

var namePattern = regexp.MustCompile(`^cobogo-core-(\d{8})-(\d{6})(?:-([a-z0-9][a-z0-9._-]{0,47}))?\.dump$`)

// File é um backup no diretório.
type File struct {
	Name string    `json:"name"`
	Size int64     `json:"size_bytes"`
	At   time.Time `json:"created_at"`
	// Label é o rótulo de um backup pedido (pre-<commit>); vazio nos automáticos.
	Label  string `json:"label"`
	SHA256 string `json:"sha256"`
}

// Labeled diz se o backup foi pedido com um rótulo (e não pelo agendamento).
func (f File) Labeled() bool { return f.Label != "" }

// ValidName diz se o texto é o nome de um backup nosso: nada de caminho, nada de
// outro arquivo. É o que a API confere antes de abrir um arquivo.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Name monta o nome de um backup.
func Name(at time.Time, label string) string {
	n := Prefix + at.UTC().Format("20060102-150405")
	if label != "" {
		n += "-" + label
	}
	return n + Suffix
}

// ParseName separa a hora e o rótulo do nome. A hora do nome é UTC.
func ParseName(name string) (time.Time, string, bool) {
	m := namePattern.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, "", false
	}
	at, err := time.ParseInLocation("20060102150405", m[1]+m[2], time.UTC)
	if err != nil {
		return time.Time{}, "", false
	}
	return at, m[3], true
}

// ValidLabel diz se um rótulo serve num nome de arquivo.
func ValidLabel(label string) bool {
	return label == "" || namePattern.MatchString(Name(time.Unix(0, 0), label))
}

// Plan decide o que guardar. Dos automáticos fica o mais novo de cada um dos
// últimos keepDaily dias e o mais novo de cada uma das últimas keepWeekly
// semanas (a semana começa na segunda); dos com rótulo ficam os KeepLabeled mais
// novos. O backup mais novo de todos nunca sai.
func Plan(files []File, now time.Time, keepDaily, keepWeekly int) (keep, remove []File) {
	now = now.UTC()
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.After(sorted[j].At) })
	kept := map[string]bool{}
	if len(sorted) > 0 {
		kept[sorted[0].Name] = true
	}
	today := now.Truncate(24 * time.Hour)
	weekStart := func(t time.Time) time.Time {
		d := t.Truncate(24 * time.Hour)
		back := (int(d.Weekday()) + 6) % 7 // segunda = 0
		return d.AddDate(0, 0, -back)
	}
	thisWeek := weekStart(now)
	seenDay, seenWeek, labeled := map[time.Time]bool{}, map[time.Time]bool{}, 0
	for _, f := range sorted { // do mais novo ao mais antigo
		if f.Labeled() {
			if labeled < KeepLabeled {
				kept[f.Name] = true
			}
			labeled++
			continue
		}
		day := f.At.UTC().Truncate(24 * time.Hour)
		if age := int(today.Sub(day).Hours() / 24); age >= 0 && age < keepDaily && !seenDay[day] {
			seenDay[day] = true
			kept[f.Name] = true
		}
		week := weekStart(f.At.UTC())
		if age := int(thisWeek.Sub(week).Hours() / (24 * 7)); age >= 0 && age < keepWeekly && !seenWeek[week] {
			seenWeek[week] = true
			kept[f.Name] = true
		}
	}
	for _, f := range sorted {
		if kept[f.Name] {
			keep = append(keep, f)
		} else {
			remove = append(remove, f)
		}
	}
	return keep, remove
}

// Status é o que o serviço grava para o painel.
type Status struct {
	// LastRunAt é a última tentativa; LastOKAt, o último backup que deu certo.
	LastRunAt *time.Time `json:"last_run_at"`
	LastOKAt  *time.Time `json:"last_ok_at"`
	LastFile  string     `json:"last_file"`
	LastError string     `json:"last_error"`
	NextRunAt *time.Time `json:"next_run_at"`
	// Retention é a política que valeu na última execução.
	KeepDaily  int `json:"keep_daily"`
	KeepWeekly int `json:"keep_weekly"`
}

// WriteStatus grava o estado de forma atômica (um leitor nunca vê meio arquivo).
func WriteStatus(dir string, st Status) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, StatusFile), raw, 0o644)
}

// ReadStatus lê o estado; sem arquivo (o serviço ainda não rodou) devolve o vazio.
func ReadStatus(dir string) (Status, error) {
	raw, err := os.ReadFile(filepath.Join(dir, StatusFile))
	if errors.Is(err, os.ErrNotExist) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return Status{}, fmt.Errorf("backup: estado ilegível: %w", err)
	}
	return st, nil
}

// Stale diz se o último backup bem-sucedido passou da idade aceita (ou não há nenhum).
func (s Status) Stale(now time.Time) bool {
	return s.LastOKAt == nil || now.Sub(*s.LastOKAt) > StaleAfter
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// List devolve os backups do diretório, do mais novo ao mais antigo, com a soma
// SHA-256 guardada ao lado de cada um.
func List(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []File{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []File{}
	for _, e := range entries {
		at, label, ok := ParseName(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		f := File{Name: e.Name(), Size: info.Size(), At: at, Label: label}
		if raw, err := os.ReadFile(filepath.Join(dir, e.Name()+".sha256")); err == nil {
			f.SHA256 = strings.Fields(string(raw) + " ")[0]
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// Remove apaga um backup e a soma dele. Só mexe em nome válido.
func Remove(dir, name string) error {
	if !ValidName(name) {
		return fmt.Errorf("backup: nome inválido: %q", name)
	}
	for _, n := range []string{name, name + ".sha256"} {
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Hash calcula o SHA-256 de um arquivo.
func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
