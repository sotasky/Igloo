#!/usr/bin/env node

import { execFileSync } from 'node:child_process'
import { chmodSync, existsSync, mkdirSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join, resolve } from 'node:path'
import { homedir } from 'node:os'

const require = createRequire(import.meta.url)

function loadPlaywright() {
  try {
    return require('playwright')
  } catch (_) {
    const root = execFileSync('npm', ['root', '-g'], { encoding: 'utf8' }).trim()
    return require(join(root, 'playwright'))
  }
}

function defaultStatePath() {
  const stateHome = process.env.XDG_STATE_HOME || join(homedir(), '.local/state')
  return join(stateHome, 'igloo/playwright-auth.json')
}

function parseArgs(argv) {
  const out = {
    baseUrl: process.env.IGLOO_BASE_URL || 'https://localhost:5001',
    storageState: process.env.IGLOO_PLAYWRIGHT_STATE || defaultStatePath(),
    pages: ['/shorts', '/bookmarks'],
    repeats: 3,
    headed: false,
    login: false,
  }

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    switch (arg) {
      case '--base-url':
        out.baseUrl = argv[++i]
        break
      case '--storage-state':
        out.storageState = argv[++i]
        break
      case '--pages':
        out.pages = argv[++i].split(',').map((s) => s.trim()).filter(Boolean)
        break
      case '--repeat':
        out.repeats = Math.max(1, Number.parseInt(argv[++i], 10) || 1)
        break
      case '--headed':
        out.headed = true
        break
      case '--login':
        out.login = true
        out.headed = true
        break
      case '--help':
      case '-h':
        printHelp()
        process.exit(0)
        break
      default:
        throw new Error(`unknown argument: ${arg}`)
    }
  }
  out.baseUrl = String(out.baseUrl || '').replace(/\/+$/, '')
  out.storageState = resolve(out.storageState)
  return out
}

function printHelp() {
  console.log(`Usage: node scripts/dev/bench_browser_pages.mjs [options]

Options:
  --login                         Open a headed browser and save auth state after login
  --headed                        Run browser visibly
  --base-url URL                  Default: https://localhost:5001
  --storage-state PATH            Default: $XDG_STATE_HOME/igloo/playwright-auth.json
  --pages /shorts,/bookmarks      Comma-separated paths to measure
  --repeat N                      Runs per page, default 3

Examples:
  node scripts/dev/bench_browser_pages.mjs --login
  node scripts/dev/bench_browser_pages.mjs --repeat 5
`)
}

function absoluteUrl(baseUrl, path) {
  if (/^https?:\/\//i.test(path)) return path
  return `${baseUrl}${path.startsWith('/') ? path : `/${path}`}`
}

function hardenStorageStatePath(storageState) {
  mkdirSync(dirname(storageState), { recursive: true, mode: 0o700 })
  chmodSync(dirname(storageState), 0o700)
  if (existsSync(storageState)) chmodSync(storageState, 0o600)
}

async function login(opts, chromium) {
  hardenStorageStatePath(opts.storageState)
  const browser = await chromium.launch({ headless: false })
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    storageState: existsSync(opts.storageState) ? opts.storageState : undefined,
  })
  const page = await context.newPage()
  await page.goto(absoluteUrl(opts.baseUrl, '/login'), { waitUntil: 'domcontentloaded' })
  console.log(`Login window opened. Complete login in the browser; waiting up to 5 minutes.`)
  await page.waitForFunction(() => window.location.pathname !== '/login', null, { timeout: 300000 })
  await context.storageState({ path: opts.storageState })
  chmodSync(opts.storageState, 0o600)
  console.log(`Saved auth state: ${opts.storageState}`)
  await browser.close()
}

async function measurePage(context, opts, pagePath, run) {
  const page = await context.newPage()
  const failed = []
  const pageErrors = []
  const consoleErrors = []
  const htmxEvents = []
  const htmxConsole = []
  const requests = new Map()
  const finished = []
  let started = 0

  await page.addInitScript(() => {
    function attr(el, name) {
      try { return el && el.getAttribute ? (el.getAttribute(name) || '') : '' } catch (_) { return '' }
    }
    function describe(el) {
      if (!el || !el.tagName) return { tag: '' }
      return {
        tag: String(el.tagName || '').toLowerCase(),
        id: attr(el, 'id'),
        class: attr(el, 'class'),
        hxGet: attr(el, 'hx-get'),
        hxPost: attr(el, 'hx-post'),
        hxDelete: attr(el, 'hx-delete'),
        hxTrigger: attr(el, 'hx-trigger'),
        hxTarget: attr(el, 'hx-target'),
        hxSwap: attr(el, 'hx-swap'),
        dataNextUrl: attr(el, 'data-next-url'),
        name: attr(el, 'name'),
        type: attr(el, 'type'),
      }
    }
    function summarizeDetail(detail) {
      detail = detail || {}
      var out = {}
      try {
        if (detail.pathInfo) {
          out.requestPath = detail.pathInfo.requestPath || ''
          out.finalRequestPath = detail.pathInfo.finalRequestPath || ''
        }
        if (detail.xhr) {
          out.status = detail.xhr.status || 0
          out.readyState = detail.xhr.readyState || 0
        }
        if (detail.error) out.error = String(detail.error && detail.error.message ? detail.error.message : detail.error)
      } catch (_) {}
      return out
    }
    function logEvent(name, event) {
      try {
        console.warn('IGLOO_HTMX_EVENT ' + JSON.stringify({
          name: name,
          target: describe(event && event.target),
          detail: summarizeDetail(event && event.detail),
        }))
      } catch (_) {}
    }
    document.addEventListener('htmx:sendAbort', function (event) { logEvent('htmx:sendAbort', event) }, true)
    document.addEventListener('htmx:syntax:error', function (event) { logEvent('htmx:syntax:error', event) }, true)
    document.addEventListener('htmx:responseError', function (event) { logEvent('htmx:responseError', event) }, true)
  })

  page.on('request', (request) => {
    requests.set(request, {
      startedAt: Date.now(),
      method: request.method(),
      postData: request.postData() || '',
    })
  })
  page.on('requestfailed', (request) => {
    const failedAt = Date.now()
    const meta = requests.get(request) || {}
    failed.push({
      url: request.url(),
      type: request.resourceType(),
      error: request.failure()?.errorText || 'request failed',
      startMs: Math.max(0, (meta.startedAt || failedAt) - started),
      method: meta.method || request.method(),
      postData: meta.postData || '',
    })
  })
  page.on('requestfinished', async (request) => {
    const meta = requests.get(request) || {}
    const started = meta.startedAt || Date.now()
    const response = await request.response().catch(() => null)
    finished.push({
      url: request.url(),
      type: request.resourceType(),
      status: response ? response.status() : 0,
      ms: Date.now() - started,
      startMs: Math.max(0, started - measureStarted),
      method: meta.method || request.method(),
      postData: meta.postData || '',
    })
  })
  page.on('pageerror', (err) => pageErrors.push(String(err && err.message ? err.message : err)))
  page.on('console', (msg) => {
    const text = msg.text()
    const marker = 'IGLOO_HTMX_EVENT '
    if (text.includes(marker)) {
      const raw = text.slice(text.indexOf(marker) + marker.length)
      try { htmxEvents.push(JSON.parse(raw)) } catch (_) { htmxConsole.push(text) }
      return
    }
    if (/htmx:sendAbort|htmx:syntax:error|sendAbort/i.test(text)) htmxConsole.push(text)
    if (msg.type() === 'error') consoleErrors.push(text)
  })

  const url = absoluteUrl(opts.baseUrl, pagePath)
  started = Date.now()
  const measureStarted = started
  const response = await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 60000 })
  const domContentLoadedMs = Date.now() - started
  await page.waitForLoadState('load', { timeout: 30000 }).catch(() => {})
  const readiness = await measureReadiness(page, pagePath, started)
  await page.waitForTimeout(750)

  const browser = await page.evaluate(() => {
    const nav = performance.getEntriesByType('navigation')[0]
    const activeShort = document.querySelector('.shorts-item.is-active')
    const activeVideo = activeShort ? activeShort.querySelector('video') : null
    return {
      url: window.location.href,
      path: window.location.pathname,
      nav: nav ? {
        startTime: nav.startTime,
        domContentLoadedEventEnd: nav.domContentLoadedEventEnd,
        loadEventEnd: nav.loadEventEnd,
        responseEnd: nav.responseEnd,
        transferSize: nav.transferSize,
      } : null,
      cards: document.querySelectorAll('.video-card').length,
      feedItems: document.querySelectorAll('[data-feed-item]').length,
      channelSections: document.querySelectorAll('.channel-section').length,
      sentinels: document.querySelectorAll('.js-infinite-scroll').length,
      htmlKB: Math.round(document.documentElement.outerHTML.length / 1024),
      scripts: document.scripts.length,
      resources: performance.getEntriesByType('resource').length,
      activeShort: activeShort ? {
        videoId: activeShort.getAttribute('data-video-id') || '',
        hasVideo: !!activeVideo,
        currentTime: activeVideo ? Number(activeVideo.currentTime || 0) : 0,
        readyState: activeVideo ? Number(activeVideo.readyState || 0) : 0,
        networkState: activeVideo ? Number(activeVideo.networkState || 0) : 0,
        paused: activeVideo ? !!activeVideo.paused : false,
        muted: activeVideo ? !!activeVideo.muted : false,
        src: activeVideo ? String(activeVideo.currentSrc || activeVideo.src || '') : '',
        error: activeVideo && activeVideo.error ? Number(activeVideo.error.code || 0) : 0,
      } : null,
    }
  })
  await page.close()

  const mediaRequests = finished
    .filter((r) => r.type === 'media' || /\/api\/media\/(stream|audio)\//.test(r.url))
    .sort((a, b) => a.startMs - b.startMs)
  const mediaFailures = failed
    .filter((r) => r.type === 'media' || /\/api\/media\/(stream|audio)\//.test(r.url))

  return {
    page: pagePath,
    run,
    status: response ? response.status() : 0,
    domContentLoadedMs,
    loadMs: browser.nav ? Math.round(browser.nav.loadEventEnd) : 0,
    responseEndMs: browser.nav ? Math.round(browser.nav.responseEnd) : 0,
    finalPath: browser.path,
    finalUrl: browser.url,
    cards: browser.cards,
    feedItems: browser.feedItems,
    channelSections: browser.channelSections,
    sentinels: browser.sentinels,
    htmlKB: browser.htmlKB,
    resources: browser.resources,
    readiness,
    activeShort: browser.activeShort,
    mediaRequests,
    mediaFailures,
    requestFailures: failed.length,
    pageErrors: pageErrors.length,
    consoleErrors: consoleErrors.length,
    consoleErrorLines: consoleErrors.slice(0, 5),
    htmxEvents,
    htmxConsole,
    slowRequests: finished
      .filter((r) => r.ms >= 750)
      .sort((a, b) => b.ms - a.ms)
      .slice(0, 5),
    badResponses: finished
      .filter((r) => r.status >= 400)
      .sort((a, b) => b.status - a.status || b.ms - a.ms)
      .slice(0, 5),
    failed: failed.slice(0, 5),
  }
}

async function measureReadiness(page, pagePath, started) {
  const path = pagePath.split('?')[0]
  const out = {}
  if (path !== '/shorts') return out

  await page.waitForFunction(() => !!document.querySelector('#shorts-layout:not(.hidden), body.shorts-open'), null, { timeout: 12000 })
    .then(() => { out.overlayMs = Date.now() - started })
    .catch(() => { out.overlayMs = null })

  await page.waitForFunction(() => !!document.querySelector('.shorts-item.is-active'), null, { timeout: 12000 })
    .then(() => { out.activeMs = Date.now() - started })
    .catch(() => { out.activeMs = null })

  const hasVideo = await page.evaluate(() => {
    const active = document.querySelector('.shorts-item.is-active')
    return !!(active && active.querySelector('video'))
  }).catch(() => false)
  if (!hasVideo) return out

  await page.waitForFunction(() => {
    const active = document.querySelector('.shorts-item.is-active')
    const video = active && active.querySelector('video')
    return !!(video && video.readyState >= 3)
  }, null, { timeout: 15000 }).then(() => { out.videoReadyMs = Date.now() - started })
    .catch(() => { out.videoReadyMs = null })

  await page.waitForFunction(() => {
    const active = document.querySelector('.shorts-item.is-active')
    const video = active && active.querySelector('video')
    return !!(video && !video.paused && !video.ended && video.currentTime > 0)
  }, null, { timeout: 15000 }).then(() => { out.videoPlayingMs = Date.now() - started })
    .catch(() => { out.videoPlayingMs = null })

  return out
}

function printResult(result) {
  const final = new URL(result.finalUrl)
  const finalPath = `${final.pathname}${final.search}`
  const parts = [
    result.page,
    `run=${result.run}`,
    `status=${result.status}`,
    `dcl=${result.domContentLoadedMs}ms`,
    `load=${result.loadMs}ms`,
    `response=${result.responseEndMs}ms`,
    `cards=${result.cards}`,
    `html=${result.htmlKB}KB`,
    `resources=${result.resources}`,
    `fail=${result.requestFailures}`,
    `pageerr=${result.pageErrors}`,
    `consoleerr=${result.consoleErrors}`,
  ]
  if (result.feedItems) parts.push(`feed=${result.feedItems}`)
  if (result.channelSections) parts.push(`sections=${result.channelSections}`)
  if (result.readiness && result.readiness.overlayMs !== undefined) {
    parts.push(`overlay=${fmtMs(result.readiness.overlayMs)}`)
    parts.push(`active=${fmtMs(result.readiness.activeMs)}`)
    parts.push(`videoReady=${fmtMs(result.readiness.videoReadyMs)}`)
    parts.push(`videoPlay=${fmtMs(result.readiness.videoPlayingMs)}`)
  }
  if (finalPath !== result.page) parts.push(`final=${finalPath}`)
  console.log(parts.join('  '))
  if (result.activeShort) {
    const s = result.activeShort
    console.log(`  activeShort id=${s.videoId || '-'} video=${s.hasVideo ? 'yes' : 'no'} ready=${s.readyState} network=${s.networkState} paused=${s.paused} muted=${s.muted} t=${s.currentTime.toFixed(2)} error=${s.error}`)
  }
  if (result.mediaRequests.length) {
    const first = result.mediaRequests[0]
    const path = first.url.replace(/^https?:\/\/[^/]+/i, '')
    console.log(`  firstMedia start=${first.startMs}ms dur=${first.ms}ms ${first.status} ${first.type} ${path}`)
    for (const req of result.mediaRequests.filter((r) => r.ms >= 750).slice(0, 5)) {
      const reqPath = req.url.replace(/^https?:\/\/[^/]+/i, '')
      console.log(`  slowMedia start=${req.startMs}ms dur=${req.ms}ms ${req.status} ${req.type} ${reqPath}`)
    }
  }
  if (result.slowRequests.length) {
    for (const req of result.slowRequests) {
      const path = req.url.replace(/^https?:\/\/[^/]+/i, '')
      console.log(`  slow ${req.ms}ms ${req.status} ${req.type} ${path}`)
    }
  }
  if (result.failed.length) {
    for (const req of result.failed) {
      const path = req.url.replace(/^https?:\/\/[^/]+/i, '')
      console.log(`  failed ${req.type} ${path}: ${req.error}`)
    }
  }
  if (result.badResponses.length) {
    for (const req of result.badResponses) {
      const path = req.url.replace(/^https?:\/\/[^/]+/i, '')
      console.log(`  bad ${req.ms}ms ${req.status} ${req.type} ${path}`)
      if (req.postData) console.log(`    body ${req.postData.slice(0, 500)}`)
    }
  }
  if (result.htmxEvents.length) {
    for (const ev of result.htmxEvents.slice(0, 8)) {
      const target = ev.target || {}
      const attrs = [
        target.id ? `#${target.id}` : target.tag,
        target.hxGet ? `hx-get=${target.hxGet}` : '',
        target.hxPost ? `hx-post=${target.hxPost}` : '',
        target.hxDelete ? `hx-delete=${target.hxDelete}` : '',
        target.hxTrigger ? `trigger=${target.hxTrigger}` : '',
      ].filter(Boolean).join(' ')
      console.log(`  htmx ${ev.name} ${attrs}`)
    }
  }
  if (result.htmxConsole.length) {
    for (const line of result.htmxConsole.slice(0, 5)) console.log(`  htmx-console ${line}`)
  }
  if (result.consoleErrorLines.length) {
    for (const line of result.consoleErrorLines.slice(0, 5)) console.log(`  console-error ${line}`)
  }
}

function fmtMs(value) {
  return value == null ? 'timeout' : `${value}ms`
}

async function main() {
  const opts = parseArgs(process.argv.slice(2))
  hardenStorageStatePath(opts.storageState)
  const { chromium } = loadPlaywright()

  if (opts.login) {
    await login(opts, chromium)
    return
  }

  const browser = await chromium.launch({ headless: !opts.headed })
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    storageState: existsSync(opts.storageState) ? opts.storageState : undefined,
  })

  let redirectedToLogin = false
  for (const pagePath of opts.pages) {
    for (let run = 1; run <= opts.repeats; run++) {
      const result = await measurePage(context, opts, pagePath, run)
      printResult(result)
      if (result.finalPath === '/login') redirectedToLogin = true
    }
  }

  await browser.close()
  if (redirectedToLogin) {
    console.error(`Redirected to /login. Run with --login first to create ${opts.storageState}`)
    process.exit(2)
  }
}

main().catch((err) => {
  console.error(err && err.stack ? err.stack : err)
  process.exit(1)
})
