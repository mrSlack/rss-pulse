import { createHash } from 'node:crypto';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import type { Plugin } from 'vite';

const placeholder = /([`"'])__PULSE_PRECACHE_MANIFEST__\1/g;

function publicFiles(dir: string, prefix = ''): string[] {
  return readdirSync(dir).flatMap((name) => {
    if (name.startsWith('.')) return [];
    const path = join(dir, name);
    return statSync(path).isDirectory() ? publicFiles(path, `${prefix}${name}/`) : [`${prefix}${name}`];
  });
}

/**
 * Builds src/sw/sw.ts into dist/sw.js (an unhashed name, so the registration
 * URL never changes) and fills in the precache manifest: every emitted file
 * and every file from public/, plus a version derived from their contents so
 * sw.js changes, and browsers install the update, whenever any file changes.
 */
export function serviceWorker(): Plugin {
  const entry = resolve(import.meta.dirname, 'src/sw/sw.ts');
  const publicDir = resolve(import.meta.dirname, 'public');
  return {
    name: 'pulse-service-worker',
    apply: 'build',
    buildStart() {
      this.emitFile({ type: 'chunk', id: entry, fileName: 'sw.js' });
    },
    generateBundle: {
      order: 'post',
      handler(_options, bundle) {
        const worker = bundle['sw.js'];
        if (!worker || worker.type !== 'chunk') return this.error('sw.js was not emitted');
        if (worker.imports.length > 0 || worker.dynamicImports.length > 0) {
          return this.error(`sw.js must be self-contained but imports ${[...worker.imports, ...worker.dynamicImports].join(', ')}`);
        }

        const hash = createHash('sha256');
        const files = new Map<string, string | Uint8Array>();
        for (const [fileName, output] of Object.entries(bundle)) {
          if (fileName === 'sw.js' || fileName.endsWith('.map')) continue;
          files.set(fileName, output.type === 'chunk' ? output.code : output.source);
        }
        for (const fileName of publicFiles(publicDir)) {
          if (!files.has(fileName)) files.set(fileName, readFileSync(join(publicDir, fileName)));
        }
        const names = [...files.keys()].sort();
        for (const name of names) hash.update(name).update('\0').update(files.get(name)!).update('\0');

        const manifest = { version: hash.digest('hex').slice(0, 16), urls: names.map((name) => `/${name}`) };
        const matches = worker.code.match(placeholder);
        if (matches?.length !== 1) return this.error('sw.js must contain the precache placeholder exactly once');
        worker.code = worker.code.replace(placeholder, () => JSON.stringify(JSON.stringify(manifest)));
      },
    },
  };
}
