import { chromium } from '@playwright/test';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const htmlPath = path.resolve(__dirname, 'render-social-preview.html');

async function main() {
  console.log('Rendering 1280x640 Joltrin Social Preview Card...');
  const browser = await chromium.launch();
  const page = await browser.newPage({
    viewport: { width: 1280, height: 640 },
    deviceScaleFactor: 2, // Retina 2x quality
  });

  await page.goto(`file://${htmlPath}`, { waitUntil: 'networkidle' });
  await page.evaluate(() => document.fonts.ready);

  const buffer = await page.screenshot({
    type: 'png',
    clip: { x: 0, y: 0, width: 1280, height: 640 },
  });

  await browser.close();

  const targetPaths = [
    path.resolve(__dirname, '../docs/assets/joltrin-social-preview.png'),
    path.resolve(__dirname, '../docs/assets/og-image.png'),
    path.resolve(__dirname, '../sop-arena/public/og-image.png'),
  ];

  // Optional paths if directories exist
  const optionalPaths = [
    path.resolve(__dirname, '../sop-arena/dist/og-image.png'),
    path.resolve(__dirname, '../_site/docs/assets/joltrin-social-preview.png'),
    path.resolve(__dirname, '../_site/docs/assets/og-image.png'),
    path.resolve(__dirname, '../_site/arena/og-image.png'),
    path.resolve(__dirname, '../_site/assets/joltrin-social-preview.png'),
    path.resolve(__dirname, '../_site/assets/og-image.png'),
  ];

  for (const p of [...targetPaths, ...optionalPaths]) {
    try {
      const dir = path.dirname(p);
      if (fs.existsSync(dir)) {
        fs.writeFileSync(p, buffer);
        console.log(`Saved: ${p}`);
      }
    } catch (e) {
      console.warn(`Skipping ${p}:`, e.message);
    }
  }

  console.log('Successfully generated Joltrin Social Preview & OG image assets!');
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
