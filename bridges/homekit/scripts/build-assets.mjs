import { copyFile, mkdir } from 'node:fs/promises';

// These files are owned by the Entity types, never hand-maintained in this package.
for (const kind of ['power', 'brightness']) {
  const destination = new URL(`../dist/schemas/${kind}/`, import.meta.url);
  await mkdir(destination, { recursive: true });
  for (const file of ['support.schema.json', 'state.schema.json', 'set-parameters.schema.json']) {
    await copyFile(
      new URL(`../../../entitytypes/${kind}v1/${file}`, import.meta.url),
      new URL(file, destination),
    );
  }
}
