# Marca do Cobogó

A logo são quatro placas de cobogó num quadrado 2×2, lidas da esquerda para a
direita e de cima para baixo: **C**, **O**, **B** e **O** (quadrado). Desenho do
autor do projeto.

## Arquivos

| Arquivo | O que é |
| --- | --- |
| `originais/` | Os arquivos como foram desenhados: a logo e cada placa separada (`c`, `o`, `b`, `o-quadrado`), em cinza e grafite, sem cor oficial. |
| `cobogo-logo.svg` | A logo **vazada**: os motivos são furos de verdade (máscara), então o fundo aparece por eles. A cor das placas é `currentColor`. Geometria idêntica ao original (placas de 100, junta de 2, traço do C de 3). |
| `cobogo-logo-pequena.svg` | A mesma logo com ajuste óptico para tamanhos pequenos (até ~40 px): junta de 10 e traço do C de 10. Na geometria original, em 32 px a junta some e o C vira um borrão. |
| `../../web/public/favicon.svg` | A versão pequena com a cor do painel: `#006194` no tema claro e `#93ccff` no escuro. |

No painel, o componente `Logo` (`web/src/components/ui.tsx`) desenha a mesma
geometria e escolhe sozinho a versão pelo tamanho. A cor das placas é um
parâmetro: o azul da marca na barra e no acesso, e um tom quase igual ao fundo no
padrão decorativo das telas de entrada.

## Cor

Não há cor oficial ainda. Como os motivos são vazados, a logo funciona com uma
cor só, a das placas, sobre qualquer fundo. Para a versão de duas cores (placas
e motivos), basta pintar os motivos por cima em vez de recortá-los, como no
original.
