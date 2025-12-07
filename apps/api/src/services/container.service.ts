import Docker from "dockerode";

const docker = new Docker({ socketPath: "/var/run/docker.sock" });

export class ContainerService {
  static async startContainer(
    projectName: string,
    imageName: string,
    internalPort: number = 3000
  ) {
    const containerName = `cobogo-${projectName}`;

    // 1. Limpeza: Verifica se já existe um container rodando com esse nome e mata ele
    // (No futuro faremos Zero Downtime, mas no MVP matamos o antigo primeiro)
    const existingContainer = docker.getContainer(containerName);
    try {
      const details = await existingContainer.inspect();
      if (details) {
        console.log(
          `[Container] Removendo container antigo: ${containerName}...`
        );
        // Force: true mata mesmo se estiver rodando
        await existingContainer.remove({ force: true });
      }
    } catch (e) {
      // Se der erro 404, é porque não existe, então segue o baile
    }

    // 2. Configurar Labels do Traefik (A Mágica do Roteamento)
    // Vamos usar o domínio: nome-do-projeto.localhost
    const domain = `${projectName}.localhost`;

    const labels = {
      "traefik.enable": "true",
      [`traefik.http.routers.${projectName}.rule`]: `Host(\`${domain}\`)`,
      [`traefik.http.services.${projectName}.loadbalancer.server.port`]: `${internalPort}`,
      // Isso permite funcionar mesmo sem HTTPS localmente
      [`traefik.http.routers.${projectName}.entrypoints`]: "web",
    };

    console.log(
      `[Container] Iniciando ${containerName} em http://${domain}...`
    );

    // 3. Criar e Rodar o Container
    const container = await docker.createContainer({
      Image: imageName,
      name: containerName,
      Labels: labels,
      HostConfig: {
        NetworkMode: "cobogo-net", // IMPORTANTE: Tem que estar na mesma rede do Traefik
        RestartPolicy: {
          Name: "unless-stopped", // Se o PC reiniciar, o app volta sozinho
        },
      },
      // Variáveis de ambiente seriam injetadas aqui (Env: ["KEY=VAL"])
      Env: [`PORT=${internalPort}`, `NODE_ENV=production`],
    });

    await container.start();
    console.log(`[Container] ${containerName} rodando com sucesso! 🚀`);

    return domain;
  }
}
