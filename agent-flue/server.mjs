import { startFlueNodeServer } from './dist/app.mjs';

const port = Number.parseInt(process.env.PORT || '5174', 10);
const server = await startFlueNodeServer({
  hostname: '127.0.0.1',
  port,
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, async () => {
    await server.stop();
    process.exitCode = 0;
  });
}
