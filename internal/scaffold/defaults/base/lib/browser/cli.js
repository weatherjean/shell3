#!/usr/bin/env node
// lib/browser/cli.js — headed Chrome automation via puppeteer-core (no MCP).
// One headed Chrome (a dedicated debugging profile) persists across calls on a
// fixed debug port; each invocation reconnects, acts, and leaves the window open.
//
// Usage: node cli.js <verb> [args]
//   open <url>            navigate the active tab
//   eval '<js-expr>'      evaluate JS in the page, print the result
//   click <selector>      click the first match
//   type <selector> <txt> type text into the first match
//   wait <selector>       wait until the selector appears (30s)
//   screenshot <path>     full-page PNG to <path>
//   pdf <path>            print the page to <path>
//   close                 close the persistent Chrome
//
// Chrome is located via $CHROME_PATH or common per-OS install paths.

const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');

let puppeteer;
try {
  puppeteer = require('puppeteer-core');
} catch (e) {
  console.error('puppeteer-core not installed. Run: (cd ' + __dirname + ' && npm i)');
  process.exit(3);
}

const PORT = 9333;
const BROWSER_URL = 'http://127.0.0.1:' + PORT;
const PROFILE = path.join(__dirname, 'profile');

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function chromePath() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;
  const byOS = {
    darwin: [
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
      '/Applications/Chromium.app/Contents/MacOS/Chromium',
    ],
    linux: [
      '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable',
      '/usr/bin/chromium', '/usr/bin/chromium-browser',
    ],
    win32: [
      'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
      'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
    ],
  };
  for (const p of byOS[process.platform] || []) {
    if (fs.existsSync(p)) return p;
  }
  throw new Error('Chrome not found; set CHROME_PATH to your Chrome binary');
}

function spawnChrome() {
  const child = spawn(chromePath(), [
    '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + PROFILE,
    '--no-first-run',
    '--no-default-browser-check',
  ], { detached: true, stdio: 'ignore' });
  child.unref();
}

async function connect() {
  try {
    return await puppeteer.connect({ browserURL: BROWSER_URL });
  } catch (e) {
    spawnChrome();
    for (let i = 0; i < 40; i++) {
      await sleep(300);
      try {
        return await puppeteer.connect({ browserURL: BROWSER_URL });
      } catch (_) { /* not up yet */ }
    }
    throw new Error('could not reach Chrome on ' + BROWSER_URL);
  }
}

async function activePage(browser) {
  const pages = await browser.pages();
  return pages.length ? pages[pages.length - 1] : await browser.newPage();
}

async function main() {
  const [verb, ...rest] = process.argv.slice(2);
  if (!verb) { console.error('usage: node cli.js <verb> [args]'); process.exit(2); }

  if (verb === 'close') {
    try {
      const b = await puppeteer.connect({ browserURL: BROWSER_URL });
      await b.close();
      console.log('closed');
    } catch (e) {
      console.log('not running');
    }
    return;
  }

  const browser = await connect();
  const page = await activePage(browser);
  let out = '';
  switch (verb) {
    case 'open':
      await page.goto(rest[0], { waitUntil: 'domcontentloaded' });
      out = 'opened ' + page.url();
      break;
    case 'eval':
      out = String(await page.evaluate(rest.join(' ')));
      break;
    case 'click':
      await page.click(rest[0]);
      out = 'clicked ' + rest[0];
      break;
    case 'type':
      await page.type(rest[0], rest.slice(1).join(' '));
      out = 'typed into ' + rest[0];
      break;
    case 'wait':
      await page.waitForSelector(rest[0], { timeout: 30000 });
      out = 'visible ' + rest[0];
      break;
    case 'screenshot':
      await page.screenshot({ path: rest[0], fullPage: true });
      out = 'screenshot ' + rest[0];
      break;
    case 'pdf':
      await page.pdf({ path: rest[0] });
      out = 'pdf ' + rest[0];
      break;
    default:
      await browser.disconnect();
      console.error('unknown verb: ' + verb);
      process.exit(2);
  }
  console.log(out);
  await browser.disconnect(); // leave the window open for the next call
}

main().catch((e) => { console.error('error: ' + e.message); process.exit(1); });
