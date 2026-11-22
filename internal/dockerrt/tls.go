package dockerrt

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"path"
	"sort"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// TLS do banco gerenciado (DEC-44). O certificado é gerado aqui, em Go, e
// gravado no volume do banco por um contêiner descartável que nunca inicia: a
// imagem do PostgreSQL não traz o openssl e nada precisa rodar para isso. O
// certificado é público; a chave fica no volume, só legível pelo postgres.

const (
	// postgresUID é o usuário `postgres` da imagem (alpine).
	postgresUID = 70
	// PostgresTLSDir é onde o certificado e a chave ficam, fora do PGDATA: o
	// initdb exige um diretório de dados vazio.
	PostgresTLSDir = domain.PostgresDataPath + "/tls"
	// PostgresPGData é o diretório de dados dos bancos com TLS: um subdiretório
	// do ponto de montagem, para que o certificado possa ficar ao lado.
	PostgresPGData = domain.PostgresDataPath + "/pgdata"

	serverCertName = "server.crt"
	serverKeyName  = "server.key"
	maxReadFile    = 64 << 10
)

// tarEntry é um item do arquivo enviado ao daemon.
type tarEntry struct {
	name     string
	body     []byte
	dir      bool
	mode     int64
	uid, gid int
}

func tarOf(entries []tarEntry) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Uid: e.uid, Gid: e.gid, ModTime: time.Now()}
		if e.dir {
			hdr.Typeflag = tar.TypeDir
			if hdr.Name[len(hdr.Name)-1] != '/' {
				hdr.Name += "/"
			}
		} else {
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if !e.dir {
			if _, err := tw.Write(e.body); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

// generateServerCert cria um certificado autoassinado para o nome do contêiner
// do banco (e `localhost`), válido por dez anos. É a sua própria âncora de
// confiança: a aplicação confia nele diretamente.
func generateServerCert(host string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host, Organization: []string{"Cobogó"}},
		DNSNames:              []string{host, "localhost"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// putPostgresTLS grava o certificado e a chave no volume do banco, sem iniciar
// nada: cria um contêiner descartável com o volume montado, copia o arquivo e o
// remove. A chave é do postgres, modo 0600.
func (r *Runtime) putPostgresTLS(ctx context.Context, spec PostgresSpec) error {
	certPEM, keyPEM, err := generateServerCert(spec.Container)
	if err != nil {
		return fmt.Errorf("dockerrt: gerar o certificado do banco: %w", err)
	}
	archive, err := tarOf([]tarEntry{
		{name: "tls", dir: true, mode: 0o700, uid: postgresUID, gid: postgresUID},
		{name: "tls/" + serverCertName, body: certPEM, mode: 0o644, uid: postgresUID, gid: postgresUID},
		{name: "tls/" + serverKeyName, body: keyPEM, mode: 0o600, uid: postgresUID, gid: postgresUID},
	})
	if err != nil {
		return fmt.Errorf("dockerrt: montar o certificado do banco: %w", err)
	}
	helper, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image: PostgresImage, Entrypoint: []string{"/bin/true"},
			Labels: map[string]string{LabelManaged: "true", LabelProject: spec.ProjectID, "cobogo.role": "tls-init"},
		},
		&container.HostConfig{
			Mounts: volumeMounts(map[string]string{domain.PostgresDataPath: spec.Volume}), NetworkMode: "none",
			CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges"},
		}, nil, nil, "")
	if err != nil {
		return fmt.Errorf("dockerrt: preparar o certificado do banco: %w", err)
	}
	defer func() {
		_ = r.cli.ContainerRemove(context.WithoutCancel(ctx), helper.ID, container.RemoveOptions{Force: true})
	}()
	if err := r.cli.CopyToContainer(ctx, helper.ID, domain.PostgresDataPath, archive, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("dockerrt: gravar o certificado no volume do banco: %w", err)
	}
	return nil
}

// ReadFile lê um arquivo pequeno de um contêiner do Cobogó do projeto. Serve
// para pegar o certificado público do banco. Contêiner sem os labels, ou de
// outro projeto, é recusado.
func (r *Runtime) ReadFile(ctx context.Context, projectID, containerName, filePath string) ([]byte, error) {
	inspect, err := r.cli.ContainerInspect(ctx, containerName)
	if err != nil {
		return nil, fmt.Errorf("dockerrt: inspecionar %s: %w", containerName, err)
	}
	if inspect.Config == nil || inspect.Config.Labels[LabelManaged] != "true" || inspect.Config.Labels[LabelProject] != projectID {
		return nil, fmt.Errorf("dockerrt: o contêiner %s não é do Cobogó neste projeto", containerName)
	}
	rc, _, err := r.cli.CopyFromContainer(ctx, inspect.ID, filePath)
	if err != nil {
		return nil, fmt.Errorf("dockerrt: ler %s de %s: %w", filePath, containerName, err)
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("dockerrt: ler %s: %w", filePath, err)
	}
	if hdr.Typeflag != tar.TypeReg || hdr.Size > maxReadFile {
		return nil, fmt.Errorf("dockerrt: %s não é um arquivo pequeno", filePath)
	}
	return io.ReadAll(io.LimitReader(tr, maxReadFile))
}

// WithFiles grava arquivos (caminho absoluto -> conteúdo) no contêiner depois de
// criado e antes de iniciar. Os arquivos são do root, só leitura. O conteúdo vem
// da plataforma, nunca do repositório.
func WithFiles(files map[string][]byte) RunOption {
	return func(s *runSpec) { s.files = files }
}

func (r *Runtime) putFiles(ctx context.Context, containerID string, files map[string][]byte) error {
	var entries []tarEntry
	dirs := map[string]bool{}
	names := make([]string, 0, len(files))
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		rel := path.Clean(p)[1:]
		for d := path.Dir(rel); d != "." && !dirs[d]; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList) // o pai sempre antes do filho
	for _, d := range dirList {
		entries = append(entries, tarEntry{name: d, dir: true, mode: 0o755})
	}
	for _, p := range names {
		entries = append(entries, tarEntry{name: path.Clean(p)[1:], body: files[p], mode: 0o444})
	}
	archive, err := tarOf(entries)
	if err != nil {
		return err
	}
	if err := r.cli.CopyToContainer(ctx, containerID, "/", archive, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("dockerrt: gravar arquivos no contêiner: %w", err)
	}
	return nil
}
