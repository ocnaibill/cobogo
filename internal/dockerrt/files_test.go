package dockerrt

import (
	"context"
	"strings"
	"testing"
)

// Arquivos da plataforma entram no contêiner antes de ele iniciar (DEC-44): o
// caminho é criado, o conteúdo é o dado, e só se lê de contêiner do projeto.
func TestWithFilesPutsTheFileInsideTheContainerBeforeItStarts(t *testing.T) {
	rt := dockerForTest(t)
	tag := idleImage(t, rt)
	ctx := context.Background()
	project := "projeto-arquivos-" + strings.ToLower(t.Name())
	const pem = "-----BEGIN CERTIFICATE-----\nconteudo\n-----END CERTIFICATE-----\n"

	res, err := rt.Run(ctx, tag, "cobogo-test-files", 3000, nil, "deploy-files",
		WithLabels(map[string]string{LabelProject: project}),
		WithFiles(map[string][]byte{"/etc/cobogo/db-ca.pem": []byte(pem)}))
	t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	if err != nil {
		t.Fatal(err)
	}
	got, err := rt.ReadFile(ctx, project, "cobogo-test-files", "/etc/cobogo/db-ca.pem")
	if err != nil || string(got) != pem {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	// Outro projeto não lê o contêiner.
	if _, err := rt.ReadFile(ctx, "outro-projeto", "cobogo-test-files", "/etc/cobogo/db-ca.pem"); err == nil {
		t.Error("a container of another project must not be readable")
	}
	// O que não é arquivo (diretório) é recusado.
	if _, err := rt.ReadFile(ctx, project, "cobogo-test-files", "/etc/cobogo"); err == nil {
		t.Error("a directory is not a small file")
	}
}

func TestGeneratedServerCertificateCarriesTheContainerName(t *testing.T) {
	certPEM, keyPEM, err := generateServerCert("cobogo-pg-abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(certPEM), "BEGIN CERTIFICATE") || !strings.Contains(string(keyPEM), "BEGIN PRIVATE KEY") {
		t.Fatal("PEM blocks missing")
	}
}
