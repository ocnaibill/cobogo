// Package config carrega a configuração de processo a partir de variáveis
// de ambiente. Segredos de runtime de aplicações publicadas (RF-10) não
// passam por aqui — isto é só a configuração do próprio Cobogó.
package config

import (
	"fmt"
	"os"

	"github.com/ocnaibill/cobogo/internal/secrets"
)

type Config struct {
	DatabaseURL string
	APIAddr     string
	// SecretKey é a chave AES-256 que cifra os valores das variáveis dos
	// projetos (DEC-24). Fica fora do banco, no ambiente do processo.
	SecretKey []byte
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		APIAddr:     envOr("API_ADDR", ":8080"),
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: variável DATABASE_URL é obrigatória")
	}
	rawKey := os.Getenv("COBOGO_SECRET_KEY")
	if rawKey == "" {
		return Config{}, fmt.Errorf("config: variável COBOGO_SECRET_KEY é obrigatória (32 bytes em base64; gere com `openssl rand -base64 32`)")
	}
	key, err := secrets.ParseKey(rawKey)
	if err != nil {
		return Config{}, fmt.Errorf("config: COBOGO_SECRET_KEY inválida: %w", err)
	}
	c.SecretKey = key
	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
