package domain

import "testing"

// Os nomes do serviço PostgreSQL já existem em volumes e contêineres de produção: a
// generalização por engine (DEC-50) não pode mudá-los.
func TestPostgresServiceNamesAreStable(t *testing.T) {
	const project = "c2f329da-ca82-4923-be8e-b326a5c03fcf"
	if got := ServiceContainerName(project); got != "cobogo-pg-c2f329daca82" {
		t.Errorf("contêiner = %s", got)
	}
	if got := ServiceVolumeName(project); got != "cobogo-vol-c2f329daca82-dc8f97c32d" {
		t.Errorf("volume = %s", got)
	}
	if ServiceContainerName(project) != ServiceContainerNameFor(project, ServicePostgres) || ServiceVolumeName(project) != ServiceVolumeNameFor(project, ServicePostgres) {
		t.Error("as variantes por engine divergem das do PostgreSQL")
	}
	if EngineDataPath(ServicePostgres) != PostgresDataPath || EngineDataPath("desconhecida") != "" {
		t.Error("EngineDataPath")
	}
}
