-- DEC-31: o endereço público e a rota de uma release. A URL é o que o
-- usuário vê; host e destino são o que o Traefik lê (vazios sem domínio).
ALTER TABLE releases ADD COLUMN url          TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN route_host   TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN route_target TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_releases_active ON releases(operational_state) WHERE operational_state = 'active';
