-- DEC-24: overrides do pipeline generalizados (campo -> valor), no lugar do
-- override único de porta. Aditivo: o valor antigo é migrado antes da remoção.

ALTER TABLE deployments ADD COLUMN overrides JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE deployments
SET overrides = jsonb_build_object('port', port_override::text)
WHERE port_override IS NOT NULL;

ALTER TABLE deployments DROP COLUMN port_override;
