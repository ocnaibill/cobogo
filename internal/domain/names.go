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

// Identidade de cada engine de serviço gerenciado (DEC-50): o código curto que entra no
// nome do contêiner e o diretório de dados, que decide o nome do volume. O que o runtime
// precisa a mais (imagem, porta, saúde, dump) está em dockerrt.Engine.
var (
	engineCode     = map[string]string{ServicePostgres: "pg"}
	engineDataPath = map[string]string{ServicePostgres: PostgresDataPath}
)

// EngineDataPath devolve o diretório de dados de uma engine, ou "" se não é provida.
func EngineDataPath(engine string) string { return engineDataPath[engine] }

// ServiceContainerNameFor é o nome do contêiner do serviço de uma engine num projeto
// (DEC-42); também é o host que a aplicação usa na rede do projeto.
func ServiceContainerNameFor(projectID, engine string) string {
	return "cobogo-" + engineCode[engine] + "-" + shortProjectID(projectID)
}

// ServiceVolumeNameFor é o volume dos dados do serviço de uma engine num projeto.
func ServiceVolumeNameFor(projectID, engine string) string {
	return VolumeName(projectID, engineDataPath[engine])
}

// ServiceContainerName é o nome do contêiner do PostgreSQL de um projeto (DEC-42).
func ServiceContainerName(projectID string) string {
	return ServiceContainerNameFor(projectID, ServicePostgres)
}

// ServiceVolumeName é o volume dos dados do PostgreSQL do projeto.
func ServiceVolumeName(projectID string) string {
	return ServiceVolumeNameFor(projectID, ServicePostgres)
}
