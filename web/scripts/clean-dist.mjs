// Limpa o build anterior em internal/web/dist, preservando o .gitkeep que o
// go:embed precisa para compilar num checkout sem frontend construído.
import { readdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const dist = fileURLToPath(new URL("../../internal/web/dist", import.meta.url));
for (const entry of readdirSync(dist)) {
  if (entry !== ".gitkeep") rmSync(join(dist, entry), { recursive: true, force: true });
}
