import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { Button } from "./ui";

// Um botão sem tipo dentro de um formulário envia o formulário. O "Cancelar" da
// exclusão de projeto fez isso; o padrão do Button agora é type="button".
describe("Button inside a form", () => {
  it("does not submit the form unless it asks to", () => {
    const submit = vi.fn((e: { preventDefault: () => void }) => e.preventDefault());
    render(
      <form onSubmit={submit}>
        <Button>Cancelar</Button>
        <Button variant="ghost" onClick={() => {}}>
          Fechar
        </Button>
        <Button type="submit">Enviar</Button>
      </form>,
    );
    fireEvent.click(screen.getByText("Cancelar"));
    fireEvent.click(screen.getByText("Fechar"));
    expect(submit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("Enviar"));
    expect(submit).toHaveBeenCalledTimes(1);
  });
});
