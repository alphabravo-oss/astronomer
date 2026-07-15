// Crawl the app + native ArgoCD UI with Playwright and capture full-page 2x
// (1920x1080) light+dark PNGs of every reachable page, including dynamic [id]
// routes (discovered by following in-app links). Runs CONC pages concurrently.
//   LOGIN_EMAIL=... LOGIN_PASSWORD=... node scripts/shoot.mjs
import { chromium } from 'playwright-core';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';

const BASE = process.env.BASE_URL || 'https://astronomer.dev.alphabravo.io';
const EMAIL = process.env.LOGIN_EMAIL;
const PASSWORD = process.env.LOGIN_PASSWORD;
const MAX = Number(process.env.MAX_PAGES || 400);
const CONC = Number(process.env.CONC || 4); // pages captured in parallel
const VIEWPORT = { width: 1920, height: 1080 };
const OUT = path.resolve(process.cwd(), '..', 'screenshots');
if (!EMAIL || !PASSWORD) throw new Error('set LOGIN_EMAIL and LOGIN_PASSWORD');

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const slug = (p) => (p.replace(/^\//, '').replace(/\//g, '_') || 'root');

// Native ArgoCD SPA views (same origin, /argocd/*). Seeded explicitly so they
// are captured even when nothing links to them; /argocd/* links found on these
// pages are followed too.
const ARGO_SEEDS = [
  '/argocd/applications',
  '/argocd/settings',
  '/argocd/settings/repos',
  '/argocd/settings/clusters',
  '/argocd/settings/projects',
  '/argocd/user-info',
];

// Keep only same-origin in-app page links; drop api/external/logout/asset links.
function keep(href) {
  if (!href || !href.startsWith('/') || href.startsWith('//')) return null;
  const pathname = href.split('#')[0].split('?')[0];
  if (!/^\/(dashboard|auth|argocd)\b/.test(pathname)) return null;
  if (/\b(logout|signout|sign-out)\b/.test(pathname)) return null;
  if (/\.(png|jpg|svg|css|js|json|ico|woff2?)$/.test(pathname)) return null;
  return pathname;
}

async function go(page, url) {
  try {
    await page.goto(url, { waitUntil: 'load', timeout: 40000 });
  } catch {
    await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 40000 }).catch(() => {});
  }
  // brief settle for charts/animations; networkidle is too slow with polling.
  await page.waitForLoadState('networkidle', { timeout: 6000 }).catch(() => {});
  await page.waitForTimeout(600);
}

async function shoot(page, pathname, theme) {
  const dir = path.join(OUT, theme);
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: path.join(dir, slug(pathname) + '.png'), fullPage: true });
  console.log(`  ${theme.padEnd(5)} ${pathname}`);
}

async function links(page) {
  const hrefs = await page.$$eval('a[href]', (els) => els.map((e) => e.getAttribute('href')));
  return [...new Set(hrefs.map(keep).filter(Boolean))];
}

// Concurrent BFS: CONC workers share a queue + seen set. Each captures `theme`
// and (when follow=true) enqueues newly discovered links. Returns visited order.
async function crawlPass(ctx, seeds, theme, { follow, order, seen }) {
  const queue = seeds.filter((s) => !seen.has(s));
  let active = 0;
  const pages = await Promise.all(Array.from({ length: CONC }, () => ctx.newPage()));
  async function worker(page) {
    while (true) {
      if (queue.length === 0) {
        if (active === 0) return;
        await sleep(50);
        continue;
      }
      const p = queue.shift(); // no await between shift and active++ -> atomic
      if (seen.has(p) || order.length >= MAX) continue;
      seen.add(p);
      active++;
      try {
        await go(page, BASE + p);
        await shoot(page, p, theme);
        order.push(p);
        if (follow) for (const l of await links(page)) if (!seen.has(l)) queue.push(l);
      } catch (e) {
        console.log(`  ! ${p} ${e.message}`);
      }
      active--;
    }
  }
  await Promise.all(pages.map(worker));
  await Promise.all(pages.map((pg) => pg.close()));
}

async function newCtx(theme, state) {
  const ctx = await browserCtxOpts(state);
  await ctx.addInitScript((t) => {
    try {
      localStorage.setItem('astronomer-theme', t); // next-themes (plain)
      localStorage.setItem('theme', JSON.stringify(t)); // ArgoCD UI (JSON-encoded)
    } catch {
      /* best-effort */
    }
  }, theme);
  return ctx;
}

let browser;
async function browserCtxOpts(state) {
  return browser.newContext({
    ignoreHTTPSErrors: true,
    deviceScaleFactor: 2,
    viewport: VIEWPORT,
    ...(state ? { storageState: state } : {}),
  });
}

async function run() {
  browser = await chromium.launch({
    executablePath: process.env.CHROME || '/usr/bin/chromium-browser',
    args: ['--no-sandbox'],
  });

  // login once, keep the authed storage state
  const login = await browserCtxOpts();
  const lp = await login.newPage();
  await lp.goto(BASE + '/auth/login', { waitUntil: 'load' });
  await lp.fill('input[type="email"]', EMAIL);
  await lp.fill('input[type="password"]', PASSWORD);
  await lp.click('button[type="submit"]');
  await lp.waitForURL('**/dashboard**', { timeout: 30000 });
  const storageState = await login.storageState();
  await login.close();

  // DARK: concurrent BFS from the dashboard + ArgoCD seeds; discovers routes.
  console.log('=== dark (crawl) ===');
  const seen = new Set();
  const order = [];
  const dctx = await newCtx('dark', storageState);
  await crawlPass(dctx, ['/dashboard', ...ARGO_SEEDS], 'dark', { follow: true, order, seen });
  await dctx.close();
  console.log(`discovered ${order.length} pages`);

  // LIGHT: re-shoot the same discovered set, concurrently (no link following).
  console.log(`=== light (${order.length} pages) ===`);
  const lctx = await newCtx('light', storageState);
  await crawlPass(lctx, order, 'light', { follow: false, order: [], seen: new Set() });
  await lctx.close();

  // AUTH pages: logged-out context (they redirect away once authed).
  const AUTH = ['/auth/login', '/auth/login/forgot-password', '/auth/login/reset-password'];
  for (const theme of ['dark', 'light']) {
    const actx = await newCtx(theme, null);
    await crawlPass(actx, AUTH, theme, { follow: false, order: [], seen: new Set() });
    await actx.close();
  }

  await browser.close();
  console.log(`\ndone -> ${OUT}  (${order.length} app/argocd pages x2 + auth)`);
}

run().catch((e) => { console.error(e); process.exit(1); });
