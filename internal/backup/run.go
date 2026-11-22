package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Dumper escreve um dump consistente do banco em w. Em produção é o `pg_dump`;
// nos testes, um texto qualquer.
type Dumper func(ctx context.Context, w io.Writer) error

// Verifier confere que o arquivo é um dump legível (em produção, `pg_restore --list`).
type Verifier func(ctx context.Context, path string) error

// Config é o que uma execução precisa.
type Config struct {
	Dir        string
	Dump       Dumper
	Verify     Verifier
	KeepDaily  int
	KeepWeekly int
	Now        func() time.Time
}

// Run faz um backup: grava num arquivo temporário, confere que abre, calcula a
// soma, põe no lugar de uma vez e só então aplica a retenção. Um backup que falha
// não deixa arquivo nenhum e nunca apaga os que já existem. label vazio é um
// backup do agendamento. Devolve o arquivo criado.
func Run(ctx context.Context, cfg Config, label string) (File, error) {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if !ValidLabel(label) {
		return File{}, fmt.Errorf("backup: rótulo inválido: %q", label)
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return File{}, err
	}
	started := now().UTC()
	name := Name(started, label)
	final := filepath.Join(cfg.Dir, name)
	tmp, err := os.CreateTemp(cfg.Dir, ".dump-*")
	if err != nil {
		return File{}, err
	}
	defer os.Remove(tmp.Name()) // some se foi renomeado; limpa se não
	if err := cfg.Dump(ctx, tmp); err != nil {
		tmp.Close()
		return File{}, fmt.Errorf("backup: gerar o dump: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return File{}, err
	}
	if err := tmp.Close(); err != nil {
		return File{}, err
	}
	info, err := os.Stat(tmp.Name())
	if err != nil || info.Size() == 0 {
		return File{}, fmt.Errorf("backup: o dump saiu vazio")
	}
	if cfg.Verify != nil {
		if err := cfg.Verify(ctx, tmp.Name()); err != nil {
			return File{}, fmt.Errorf("backup: o dump não abre: %w", err)
		}
	}
	sum, err := Hash(tmp.Name())
	if err != nil {
		return File{}, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return File{}, err
	}
	// A soma primeiro: um arquivo de backup sem soma nunca aparece.
	if err := writeAtomic(final+".sha256", []byte(sum+"  "+name+"\n"), 0o644); err != nil {
		return File{}, err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		_ = os.Remove(final + ".sha256")
		return File{}, err
	}
	file := File{Name: name, Size: info.Size(), At: started, Label: label, SHA256: sum}
	return file, nil
}

// Prune aplica a retenção ao diretório e devolve o que apagou. Só apaga nomes de
// backup válidos: qualquer outro arquivo ali é deixado em paz.
func Prune(dir string, now time.Time, keepDaily, keepWeekly int) ([]string, error) {
	files, err := List(dir)
	if err != nil {
		return nil, err
	}
	_, remove := Plan(files, now, keepDaily, keepWeekly)
	var gone []string
	for _, f := range remove {
		if err := Remove(dir, f.Name); err != nil {
			return gone, err
		}
		gone = append(gone, f.Name)
	}
	return gone, nil
}
