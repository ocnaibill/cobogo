package backup

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Backup dos dados de um projeto (DEC-47, parte B): onde ficam os arquivos, o que
// a retenção apaga e como o backup vira um .tar para baixar.

const (
	// ProjectsDir é a pasta dos backups de projeto dentro do volume de backups.
	ProjectsDir = "projects"
	// ManifestFile e SumsFile acompanham os arquivos de cada backup de projeto.
	ManifestFile = "manifest.json"
	SumsFile     = "sha256sums.txt"
	// DatabaseFile é o dump do PostgreSQL gerenciado.
	DatabaseFile = "database.dump"
	// ManifestVersion é a versão do formato do manifest.
	ManifestVersion = 1
)

// ProjectDir é a pasta de um backup de projeto: <raiz>/projects/<projeto>/<backup>.
func ProjectDir(root string, projectID, backupID uuid.UUID) string {
	return filepath.Join(root, ProjectsDir, projectID.String(), backupID.String())
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// VolumeFileName é o nome do arquivo de um volume: derivado do caminho do contêiner
// (um sufixo da soma evita que dois caminhos de aparência parecida colidam), nunca
// de texto livre.
func VolumeFileName(path string) string {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(path), "-"), "-")
	if slug == "" {
		slug = "raiz"
	}
	if len(slug) > 40 {
		slug = slug[:40]
	}
	sum := sha256.Sum256([]byte(path))
	return "volume-" + slug + "-" + hex.EncodeToString(sum[:3]) + ".tar.gz"
}

var artifactName = regexp.MustCompile(`^(volume-[a-z0-9-]+-[0-9a-f]{6}\.tar\.gz|database\.dump|manifest\.json|sha256sums\.txt)$`)

// ValidArtifact diz se o nome é o de um arquivo que um backup de projeto pode ter.
func ValidArtifact(name string) bool { return artifactName.MatchString(name) }

// PrunePlan decide quais backups de projeto saem da retenção: só os bons, do mais
// antigo ao mais novo, até sobrarem `keep`. Os `before_restore` contam no limite mas
// **nunca saem por aqui** (a restauração depende deles; só a pessoa os apaga). Se só
// restam esses, nada sai.
func PrunePlan(backups []domain.ProjectBackup, keep int) []uuid.UUID {
	good := make([]domain.ProjectBackup, 0, len(backups))
	for _, b := range backups {
		if b.State == domain.BackupSucceeded && !b.DeleteRequested {
			good = append(good, b)
		}
	}
	sort.Slice(good, func(i, j int) bool { return good[i].CreatedAt.Before(good[j].CreatedAt) })
	excess := len(good) - keep
	var out []uuid.UUID
	for _, b := range good {
		if excess <= 0 {
			break
		}
		if b.Reason == domain.BackupBeforeRestore {
			continue
		}
		out = append(out, b.ID)
		excess--
	}
	return out
}

// FileInfo calcula tamanho e soma de um arquivo.
func FileInfo(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// WriteManifest grava o manifest.json e o sha256sums.txt de um backup de projeto.
func WriteManifest(dir string, m domain.BackupManifest) error {
	m.Version = ManifestVersion
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, ManifestFile), raw, 0o644); err != nil {
		return err
	}
	var sums strings.Builder
	for _, it := range m.Items {
		fmt.Fprintf(&sums, "%s  %s\n", it.SHA256, it.File)
	}
	return writeAtomic(filepath.Join(dir, SumsFile), []byte(sums.String()), 0o644)
}

// ReadManifest lê o manifest.json de um backup de projeto.
func ReadManifest(dir string) (domain.BackupManifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return domain.BackupManifest{}, err
	}
	var m domain.BackupManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return domain.BackupManifest{}, fmt.Errorf("backup: manifest ilegível: %w", err)
	}
	return m, nil
}

// WriteArchive escreve a pasta de um backup de projeto como um .tar, em fluxo, sem
// arquivo temporário: só os arquivos de um backup (nome fechado, arquivo comum, sem
// seguir link) e o manifest primeiro. O tar não é comprimido: os volumes já são
// .tar.gz e o dump já é comprimido.
func WriteArchive(w io.Writer, dir, prefix string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if ValidArtifact(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == ManifestFile) != (names[j] == ManifestFile) {
			return names[i] == ManifestFile
		}
		return names[i] < names[j]
	})
	tw := tar.NewWriter(w)
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: prefix + "/" + name, Mode: 0o644, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			f.Close()
			return err
		}
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	return tw.Close()
}

// ArchiveSize é o tamanho do .tar que WriteArchive vai escrever (cabeçalhos e
// preenchimento incluídos), para o Content-Length e o progresso do download.
func ArchiveSize(dir, prefix string) (int64, error) {
	cw := &countWriter{}
	if err := WriteArchive(cw, dir, prefix); err != nil {
		return 0, err
	}
	return cw.n, nil
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// FreeBytes é o espaço livre no sistema de arquivos de dir.
func FreeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// RemoveProjectDir apaga a pasta de um backup de projeto. Só mexe dentro de
// <raiz>/projects: o caminho é montado de ids, nunca de texto livre.
func RemoveProjectDir(root string, projectID, backupID uuid.UUID) error {
	dir := ProjectDir(root, projectID, backupID)
	if !strings.HasPrefix(dir, filepath.Join(root, ProjectsDir)+string(filepath.Separator)) {
		return fmt.Errorf("backup: caminho fora da pasta de backups")
	}
	return os.RemoveAll(dir)
}
