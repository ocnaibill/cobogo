-- DEC-53 F: duas aplicações podem dividir a mesma pasta quando ela mistura Python e
-- JavaScript (um Django na raiz com o React ao lado). O ecossistema fixa qual das duas
-- esta aplicação é; vazio quer dizer "detectar", como sempre foi.
ALTER TABLE projects ADD COLUMN ecosystem TEXT NOT NULL DEFAULT '';
