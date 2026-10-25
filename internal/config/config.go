// Package config carrega a configuração de processo a partir de variáveis
// de ambiente. Segredos de runtime de aplicações publicadas (RF-10) não
// passam por aqui — isto é só a configuração do próprio Cobogó.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL string
	APIAddr     string
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		APIAddr:     envOr("API_ADDR", ":8080"),
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: variável DATABASE_URL é obrigatória")
	}
	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
