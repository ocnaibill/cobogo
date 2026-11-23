package dockerrt

import (
	"context"
	"fmt"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Engine é o que muda de um serviço de dados gerenciado para outro (DEC-50): a
// imagem, a porta, o diretório de dados, a inicialização, a saúde, o dump e a
// restauração. O ciclo de vida (criar, iniciar, esperar, remover) é o mesmo para todas.
type Engine struct {
	Name  string // domain.Service*
	Label string // o nome para a pessoa
	// Image é a imagem oficial, fixada por digest.
	Image    string
	DataPath string
	Port     int
	// User e Database são os da aplicação, criados na inicialização.
	User, Database string
	// UID é o usuário dos contêineres descartáveis de dump e de restauração: o da
	// imagem, para ler e gravar com a mesma posse.
	UID string
	// Limites fixos do serviço gerenciado (DEC-42 item 3).
	MemoryMB int
	CPUs     float64
	Pids     int64
	// Args são os argumentos do servidor (depois da imagem), antes dos do TLS.
	Args []string
	// CapAdd são as capacidades que a imagem usa para ajustar a posse do diretório de
	// dados e largar o root; nada além.
	CapAdd []string
	// Health é o teste do healthcheck; o próprio daemon o roda (o worker não usa exec).
	Health []string
	// InitEnv são as variáveis de inicialização da imagem; a senha entra aqui, nunca na
	// linha de comando.
	InitEnv func(password string) []string
	// TLS prepara o TLS do serviço e devolve as variáveis e os argumentos a mais; nil
	// quando a engine não o suporta.
	TLS func(ctx context.Context, r *Runtime, spec ServiceSpec) (env, args []string, err error)
	// ClientEnv são as variáveis dos contêineres descartáveis de dump e de restauração
	// (a senha vai por ambiente).
	ClientEnv func(password string) []string
	// DumpCommand é o comando de dump para o arquivo (dentro de /out).
	DumpCommand func(host, file string) []string
	// RestoreScript é o script de `sh -c` que restaura o dump: $1 é o arquivo (dentro de
	// /out) e $2 o host do serviço.
	RestoreScript string
}

var engines = map[string]Engine{
	domain.ServicePostgres: postgresEngine,
	domain.ServiceMySQL:    mysqlEngine,
}

// EngineFor devolve a especificação de uma engine, ou um erro se o Cobogó não a provê.
func EngineFor(name string) (Engine, error) {
	e, ok := engines[name]
	if !ok {
		return Engine{}, fmt.Errorf("dockerrt: a engine %q não é provida pelo Cobogó", name)
	}
	return e, nil
}

// UtilityImage é a imagem dos contêineres descartáveis que só precisam de `tar` e de
// `sh` (copiar volumes). É a do PostgreSQL por já estar baixada e fixada.
const UtilityImage = PostgresImage

var postgresEngine = Engine{
	Name: domain.ServicePostgres, Label: "PostgreSQL",
	Image: PostgresImage, DataPath: domain.EngineDataPath(domain.ServicePostgres), Port: 5432,
	User: "app", Database: "app", UID: "70",
	MemoryMB: 256, CPUs: 0.5, Pids: 128,
	CapAdd: []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID"},
	Health: []string{"CMD-SHELL", "pg_isready -U app -d app -h 127.0.0.1"},
	InitEnv: func(password string) []string {
		return []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=" + password}
	},
	TLS: func(ctx context.Context, r *Runtime, spec ServiceSpec) ([]string, []string, error) {
		if err := r.putPostgresTLS(ctx, spec); err != nil {
			return nil, nil, err
		}
		return []string{"PGDATA=" + PostgresPGData},
			[]string{"-c", "ssl=on", "-c", "ssl_cert_file=" + PostgresTLSDir + "/" + serverCertName, "-c", "ssl_key_file=" + PostgresTLSDir + "/" + serverKeyName},
			nil
	},
	ClientEnv: func(password string) []string { return []string{"PGPASSWORD=" + password} },
	DumpCommand: func(host, file string) []string {
		return []string{"pg_dump", "--format=custom", "--no-owner", "--no-acl", "-h", host, "-U", "app", "-d", "app", "-f", "/out/" + file}
	},
	// Numa transação só: troca o esquema `public` pelo do dump; se qualquer comando falhar,
	// nada muda.
	RestoreScript: `set -e; set -o pipefail
pg_restore --list "/out/$1" > /dev/null
{ echo 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;'; pg_restore --no-owner --no-acl -f - "/out/$1"; } | psql -q -v ON_ERROR_STOP=1 --single-transaction -h "$2" -U app -d app`,
}

// MySQLImage é a imagem oficial do MySQL na versão LTS (8.4), fixada por digest.
const MySQLImage = "mysql:8.4@sha256:a81d043930e51745522bd5c430961cf8286f0a23f0a9a2a6e0108f7ea71b0902"

// mysqlEngine: MySQL LTS sem TLS na primeira versão (DEC-50). O banco só existe na rede privada
// do projeto, e o driver do Django não lê a CA por variável de ambiente.
//
// A senha do administrador (root) é aleatória e ninguém a conhece; a aplicação usa o usuário
// `app`, dono só do banco `app`. O plugin mysql_native_password fica ligado e é o padrão dos usuários
// novos: sem TLS, o caching_sha2_password (o padrão do 8.4) exige uma troca de chave que muitos
// drivers (mysqlclient com a libmariadb, PyMySQL sem a biblioteca de criptografia) não fazem.
var mysqlEngine = Engine{
	Name: domain.ServiceMySQL, Label: "MySQL",
	Image: MySQLImage, DataPath: domain.EngineDataPath(domain.ServiceMySQL), Port: 3306,
	User: "app", Database: "app", UID: "999",
	MemoryMB: 512, CPUs: 1, Pids: 256,
	Args:   []string{"--character-set-server=utf8mb4", "--collation-server=utf8mb4_0900_ai_ci", "--mysql-native-password=ON", "--authentication-policy=mysql_native_password"},
	CapAdd: []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID"},
	// O mysqladmin ping responde 0 com o servidor no ar, ainda que sem a senha; na
	// inicialização o servidor temporário só ouve no socket, então o TCP só responde no fim.
	Health: []string{"CMD-SHELL", "mysqladmin ping -h 127.0.0.1 --silent"},
	InitEnv: func(password string) []string {
		return []string{"MYSQL_RANDOM_ROOT_PASSWORD=yes", "MYSQL_DATABASE=app", "MYSQL_USER=app", "MYSQL_PASSWORD=" + password}
	},
	ClientEnv: func(password string) []string { return []string{"MYSQL_PWD=" + password} },
	DumpCommand: func(host, file string) []string {
		return []string{"mysqldump", "--single-transaction", "--routines", "--triggers", "--no-tablespaces", "--default-character-set=utf8mb4",
			"-h", host, "-u", "app", "--result-file=/out/" + file, "app"}
	},
	// O MySQL não troca o esquema numa transação só (o DDL confirma sozinho): a restauração confere
	// que o dump está completo, recria o banco e carrega. O backup de segurança que a restauração
	// faz antes é o caminho de volta.
	RestoreScript: `set -e; set -o pipefail
grep -q -- '-- Dump completed' "/out/$1"
{ echo 'DROP DATABASE IF EXISTS app; CREATE DATABASE app CHARACTER SET utf8mb4; USE app;'; cat "/out/$1"; } | mysql -h "$2" -u app --batch`,
}
