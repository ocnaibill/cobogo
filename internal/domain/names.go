package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Nomes de recursos do Docker derivados do projeto, nunca de texto do
// repositório. Ficam aqui para a API e o worker concordarem sem a API importar o
// SDK do Docker.

func shortProjectID(projectID string) string {
	id := strings.ReplaceAll(projectID, "-", "")
	if len(id) > 12 {
		id = id[:12]
	}
	return id
}

// VolumeName é o nome do volume de um caminho de um projeto (DEC-41).
func VolumeName(projectID, path string) string {
	sum := sha256.Sum256([]byte(path))
	return "cobogo-vol-" + shortProjectID(projectID) + "-" + hex.EncodeToString(sum[:])[:10]
}

// PostgresDataPath é onde a imagem do PostgreSQL guarda os dados.
const PostgresDataPath = "/var/lib/postgresql/data"

// ServiceContainerName é o nome do contêiner do PostgreSQL de um projeto (DEC-42);
// também é o host que a aplicação usa na rede do projeto.
func ServiceContainerName(projectID string) string { return "cobogo-pg-" + shortProjectID(projectID) }

// ServiceVolumeName é o volume dos dados do PostgreSQL do projeto.
func ServiceVolumeName(projectID string) string { return VolumeName(projectID, PostgresDataPath) }
