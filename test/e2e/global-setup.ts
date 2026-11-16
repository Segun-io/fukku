import { connect, createServer, type Server } from "node:net";
import { controlPort, proxyPort } from "./helpers/env";

// Fukku's publicBaseUrl and the proxy's sign-in redirect name http://127.0.0.1:18080, the
// address a person uses on the host. Inside the runner's container that address is not the
// proxy, so FUKKU_E2E_FORWARD_TO (the proxy's compose service name) makes this process forward
// the two ports of the contract to it. With the ports published on the host it is unset and
// nothing is forwarded.
function forward(listenPort: number, host: string, port: number): Promise<Server> {
  const server = createServer((client) => {
    const upstream = connect(port, host);
    client.pipe(upstream).pipe(client);
    const end = () => {
      client.destroy();
      upstream.destroy();
    };
    client.on("error", end);
    upstream.on("error", end);
    client.on("close", end);
    upstream.on("close", end);
  });
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(listenPort, "127.0.0.1", () => resolve(server));
  });
}

export default async function globalSetup() {
  const target = process.env.FUKKU_E2E_FORWARD_TO;
  if (!target) return;
  const servers = await Promise.all([
    forward(proxyPort, target, 8080),
    forward(controlPort, target, 8081),
  ]);
  return async () => {
    await Promise.all(servers.map((s) => new Promise((done) => s.close(done))));
  };
}
