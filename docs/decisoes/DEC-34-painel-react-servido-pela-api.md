# DEC-34 — Painel React servido pela própria API

> **Estado:** decidida com o autor em 20/11/2026; implementada. Fecha a interface da D0 (M2).
>
> **Escopo:** como o painel é construído, servido e protegido, e como ele acompanha um deploy.
>
> **Relaciona-se com:** DEC-27 (sessão e origem), DEC-28 (contrato e eventos), DEC-32 (implantação), §18 do mestre (telas TEL-01 a TEL-07).

## Decisão

1. **SPA estática** com React, Vite e TypeScript em `web/`. Não Next.js: não há SSR nem um segundo servidor Node.
2. **Servida pela própria API**, embutida no binário (`internal/web`, `go:embed`). Mesmo domínio: sem CORS, com o cookie `__Host-`, a conferência de `Origin` e os cabeçalhos de segurança da API valendo como estão. Sem contêiner novo nem rota nova no Traefik.
3. **Rotas do cliente:** um `GET` que não é `/api/` e vem de um navegador (`Accept` com `text/html`) recebe o `index.html`; arquivo do build é servido como está; o resto recebe o 404 em JSON. Só os arquivos com hash (`assets/`) ficam em cache longo. Pastas não são listadas e arquivos ocultos não são servidos.
4. **CSP estrita** em toda resposta: só o próprio domínio para script, estilo, imagem e conexão; nada inline, nada de `eval`, `frame-ancestors 'none'`.
5. **Cliente gerado do contrato** (`npm run gen:api` sobre `docs/api/openapi.yaml`): os tipos das respostas não são escritos à mão.
6. **Acompanhamento por consulta** a cada 2 s com `after` e `settled` (DEC-28), mais lento quando o deploy assenta, e retomado quando uma pendência é respondida.
7. **Build:** `npm run build` escreve em `internal/web/dist`; a imagem da API tem um estágio Node que o faz. Num checkout sem build, a API sobe e responde 503 explicando como construir.
8. **Visual em tokens** (`web/src/styles/tokens.css`): as telas do Stitch são ilustrativas; o painel adota o visual delas sobre o que a API de fato entrega.

## Limites

- Sem SSE: a atualização chega em até 2 s.
- A saúde em tempo real das aplicações depende do coletor (MVP); hoje o painel mostra o resultado do último deploy.
- As telas de serviços, histórico comparado e ambiente (TEL-08 a TEL-11) são do MVP.
