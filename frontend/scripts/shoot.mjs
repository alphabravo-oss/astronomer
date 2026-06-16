// Crawl the app with Playwright and capture full-page 2x light+dark PNGs of
// every reachable page, including dynamic [id] routes (discovered by following
// in-app links). Creds via env so nothing is hardcoded:
//   LOGIN_EMAIL=... LOGIN_PASSWORD=... node scripts/shoot.mjs
import { chromium } from 'playwright-core';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';

const BASE = process.env.BASE_URL || 'https://astronomer.dev.alphabravo.io';
const EMAIL = process.env.LOGIN_EMAIL;
const PASSWORD = process.env.LOGIN_PASSWORD;
const MAX = Number(process.env.MAX_PAGES || 400); // safety cap
const OUT = path.resolve(process.cwd(), '..', 'screenshots');
if (!EMAIL || !PASSWORD) throw new Error('set LOGIN_EMAIL and LOGIN_PASSWORD');

const slug = (p) => (p.replace(/^\//, '').replace(/\//g, '_') || 'root');

// Keep only same-origin in-app page links; drop api/external/logout/asset links.
function keep(href) {
  if (!href || !href.startsWith('/') || href.startsWith('//')) return null;
  const pathname = href.split('#')[0].split('?')[0];
  if (!/^\/(dashboard|auth)\b/.test(pathname)) return null;
  if (/\b(logout|signout|sign-out)\b/.test(pathname)) return null;
  if (/\.(png|jpg|svg|css|js|json|ico)$/.test(pathname)) return null;
  return pathname;
}

async function go(page, url) {
  try {
    await page.goto(url, { waitUntil: 'networkidle', timeout: 40000 });
  } catch {
    await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 40000 }).catch(() => {});
  }
  await page.waitForTimeout(1100); // let charts/animations settle
}

async function shoot(page, pathname, theme) {
  const dir = path.join(OUT, theme);
  await mkdir(dir, { recursive: true });
  const file = path.join(dir, slug(pathname) + '.png');
  await page.screenshot({ path: file, fullPage: true });
  console.log(`  ${theme.padEnd(5)} ${pathname}`);
}

async function links(page) {
  const hrefs = await page.$$eval('a[href]', (els) => els.map((e) => e.getAttribute('href')));
  return [...new Set(hrefs.map(keep).filter(Boolean))];
}

async function run() {
  const browser = await chromium.launch({
    executablePath: process.env.CHROME || '/usr/bin/chromium-browser',
    args: ['--no-sandbox'],
  });

  // --- login once, keep the authed storage state ---
  const VIEWPORT = { width: 1920, height: 1080 };
  const login = await browser.newContext({ ignoreHTTPSErrors: true, deviceScaleFactor: 2, viewport: VIEWPORT });
  const lp = await login.newPage();
  await lp.goto(BASE + '/auth/login', { waitUntil: 'networkidle' });
  await lp.fill('input[type="email"]', EMAIL);
  await lp.fill('input[type="password"]', PASSWORD);
  await lp.click('button[type="submit"]');
  await lp.waitForURL('**/dashboard**', { timeout: 30000 });
  const storageState = await login.storageState();
  await login.close();

  const newCtx = async (theme, state) => {
    const ctx = await browser.newContext({
      ignoreHTTPSErrors: true,
      deviceScaleFactor: 2,
      viewport: VIEWPORT,
      ...(state ? { storageState: state } : {}),
    });
    await ctx.addInitScript((t) => {
      try { localStorage.setItem('astronomer-theme', t); } catch {}
    }, theme);
    return ctx;
  };

  // --- DARK pass: BFS crawl from /dashboard, screenshot + discover links ---
  console.log('=== dark (crawl) ===');
  const dctx = await newCtx('dark', storageState);
  const dpage = await dctx.newPage();
  const seen = new Set();
  const order = [];
  const queue = ['/dashboard'];
  while (queue.length && order.length < MAX) {
    const p = queue.shift();
    if (seen.has(p)) continue;
    seen.add(p);
    await go(dpage, BASE + p);
    // skip if auth redirected us away (shouldn't happen while authed)
    await shoot(dpage, p, 'dark');
    order.push(p);
    for (const l of await links(dpage)) if (!seen.has(l)) queue.push(l);
  }
  await dctx.close();
  if (order.length >= MAX) console.log(`(hit MAX_PAGES=${MAX}; ${queue.length} links left unvisited)`);

  // --- LIGHT pass: re-shoot the discovered set ---
  console.log(`\n=== light (${order.length} pages) ===`);
  const lctx = await newCtx('light', storageState);
  const lpage = await lctx.newPage();
  for (const p of order) {
    await go(lpage, BASE + p);
    await shoot(lpage, p, 'light');
  }
  await lctx.close();

  // --- AUTH pages: logged-out context (redirect away once authed) ---
  const AUTH = ['/auth/login', '/auth/login/forgot-password', '/auth/login/reset-password'];
  for (const theme of ['dark', 'light']) {
    const actx = await newCtx(theme, null);
    const ap = await actx.newPage();
    for (const p of AUTH) {
      await go(ap, BASE + p);
      await shoot(ap, p, theme);
    }
    await actx.close();
  }

  await browser.close();
  console.log(`\ndone -> ${OUT}  (${order.length} app pages x2 + auth)`);
}

run().catch((e) => { console.error(e); process.exit(1); });
