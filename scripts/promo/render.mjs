// Draws the scene at 30 frames per second and encodes it, so the video is the
// same every time it is built from the same output.
//
//   node scripts/promo/render.mjs <scene-data.json> <out-dir> [seconds]
import { chromium } from '@playwright/test';
import { readFileSync, mkdirSync, rmSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { pathToFileURL, fileURLToPath } from 'node:url';
import path from 'node:path';

const [dataPath, outDir, secs] = process.argv.slice(2);
if (!dataPath || !outDir) {
  console.error('usage: node scripts/promo/render.mjs <scene-data.json> <out-dir> [seconds]');
  process.exit(2);
}
const FPS = 30;
const DURATION = Number(secs || 43);
const here = path.dirname(fileURLToPath(import.meta.url));
const frames = path.join(outDir, 'frames');
rmSync(frames, { recursive: true, force: true });
mkdirSync(frames, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1080, height: 1080 }, deviceScaleFactor: 1 });
await page.goto(pathToFileURL(path.join(here, 'scene.html')).href);
await page.evaluate((d) => window.setData(d), JSON.parse(readFileSync(dataPath, 'utf8')));
const n = Math.round(DURATION * FPS);
for (let i = 0; i < n; i++) {
  await page.evaluate((t) => window.render(t), i / FPS);
  await page.screenshot({ path: path.join(frames, `f${String(i).padStart(5, '0')}.jpg`), type: 'jpeg', quality: 94 });
  if (i % 150 === 0) process.stdout.write(`frame ${i}/${n}\n`);
}
await browser.close();
console.log('frames done:', n);
