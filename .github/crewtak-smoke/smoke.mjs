// CrewTAK smoke test. Runs in CI against a throwaway container started from
// the freshly built image, seeds a test account with a few tasks, and saves
// screenshots plus visible page text for review.
import fs from 'node:fs'

import { chromium } from 'playwright'

const BASE = process.env.BASE_URL || 'http://localhost:2021'
const OUT = process.env.OUT_DIR || 'smoke-out'
const USER = { username: 'crewtest', password: 'smoke-test-pass-1', email: 'crewtest@example.com', displayName: 'Crew Test' }

fs.mkdirSync(OUT, { recursive: true })
const report = []
const log = (...a) => {
  const line = a.join(' ')
  console.log(line)
  report.push(line)
}

const api = async (path, opts = {}, token) => {
  const res = await fetch(`${BASE}/api/v1${path}`, {
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(opts.headers || {}),
    },
  })
  const text = await res.text()
  let body
  try {
    body = JSON.parse(text)
  } catch {
    body = text
  }
  if (!res.ok) log(`API ${opts.method || 'GET'} ${path} -> ${res.status} ${text.slice(0, 200)}`)
  return body
}

const at = (dayOffset, h, m) => {
  const d = new Date()
  d.setDate(d.getDate() + dayOffset)
  d.setHours(h, m, 0, 0)
  return d.toISOString()
}

// --- seed data -------------------------------------------------------------
await api('/auth/', { method: 'POST', body: JSON.stringify(USER) })
const login = await api('/auth/login', {
  method: 'POST',
  body: JSON.stringify({ username: USER.username, password: USER.password }),
})
const token = login.access_token || login.token
log('login ok:', Boolean(token))

const chore = (name, frequencyType, nextDueDate, extra = {}) =>
  api(
    '/chores/',
    {
      method: 'POST',
      body: JSON.stringify({
        name,
        frequencyType,
        frequency: 1,
        nextDueDate,
        assignees: [],
        assignedTo: null,
        assignStrategy: 'no_assignee',
        isRolling: false,
        isActive: true,
        notification: false,
        ...extra,
      }),
    },
    token,
  )

const ids = []
for (const [name, type, due] of [
  ['Daily Truck Inspections/SCBA Inspections', 'daily', at(0, 23, 59)],
  ['Crash Phone Test with ATCT', 'daily', at(-1, 10, 0)],
  ['Wash and Detail Airport 5132', 'weekly', at(0, 23, 59)],
  ['Take out Station Garbages', 'daily', at(0, 21, 0)],
  ['Water Softener Salt Check', 'weekly', at(1, 23, 59)],
  ['5133 MEDBAG Check', 'once', at(3, 23, 59)],
]) {
  const r = await chore(name, type, due)
  ids.push(r?.res ?? r?.id ?? r)
}
log('created chores:', JSON.stringify(ids))
// Complete one task so the log and "done this shift" have something.
const choresList = await api('/chores/', {}, token)
const first = (choresList.res || []).find(c => c.name.startsWith('Daily Truck'))
if (first) {
  await api(`/chores/${first.id}/do`, { method: 'POST', body: JSON.stringify({ note: 'All good' }) }, token)
}
// Time a task with start -> complete so the log has a duration.
const timed = (choresList.res || []).find(c => c.name.startsWith('Wash and Detail'))
if (timed) {
  await api(`/chores/${timed.id}/start`, { method: 'PUT' }, token)
  await new Promise(r => setTimeout(r, 4000))
  await api(`/chores/${timed.id}/do`, { method: 'POST', body: JSON.stringify({}) }, token)
}

// --- browser ----------------------------------------------------------------
const browser = await chromium.launch()
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  timezoneId: 'America/Chicago',
  locale: 'en-US',
})
const page = await context.newPage()
const consoleErrors = []
page.on('console', m => m.type() === 'error' && consoleErrors.push(m.text()))
page.on('pageerror', e => consoleErrors.push(`pageerror: ${e.message}`))

await page.goto(`${BASE}/login`)
await page.waitForTimeout(1500)
await page.screenshot({ path: `${OUT}/00-login.png` })
fs.writeFileSync(`${OUT}/00-login.txt`, await page.evaluate(() => document.body.innerText))
await page.getByPlaceholder(/username/i).fill(USER.username)
await page.getByPlaceholder(/password/i).fill(USER.password)
await page.getByRole('button', { name: /sign in/i }).click()
await page.waitForURL(u => !u.pathname.startsWith('/login'), { timeout: 20000 }).catch(() => {})
await page.waitForTimeout(2500)
log('after login url:', page.url())

const shot = async (name, path, opts = {}) => {
  if (path) await page.goto(`${BASE}${path}`)
  await page.waitForTimeout(opts.wait ?? 2500)
  if (opts.before) await opts.before().catch(e => log(`  before() failed: ${e.message}`))
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: opts.fullPage ?? false })
  const text = await page.evaluate(() => document.body.innerText)
  fs.writeFileSync(`${OUT}/${name}.txt`, `URL: ${page.url()}\n\n${text}`)
  log(`shot ${name}: ${page.url()}`)
}

await shot('01-home', '/home')
await shot('02-tasks', '/chores')
await shot('03-create', '/chores/create', { fullPage: true })
await shot('04-create-custom-repeat', null, {
  before: async () => {
    await page.getByRole('combobox', { name: /schedule type/i }).first().click()
    await page.getByRole('option', { name: /repeating task/i }).click()
    await page.waitForTimeout(600)
    // Show the custom options too (interval / days of week / day of month).
    await page.getByText('Custom', { exact: true }).first().click().catch(() => {})
    await page.waitForTimeout(600)
  },
  fullPage: true,
  wait: 300,
})
await shot('05-quick-add', '/chores?add_task=1', { wait: 3000 })
await shot('06-task-view', first ? `/chores/${first.id}` : '/chores')
await shot('06b-task-menu', null, {
  before: async () => {
    await page.getByRole('button', { name: /^more$/i }).first().click()
    await page.waitForTimeout(600)
  },
  wait: 200,
})
await shot('07-log', '/log')
await shot('08-settings', '/settings', { fullPage: true })
await shot('09-activities', '/activities', { fullPage: true })
await shot('10-hidden-route-points', '/points')
await shot('11-hidden-route-things', '/things')
// Nav drawer
await page.goto(`${BASE}/chores`)
await page.waitForTimeout(2000)
await page.getByRole('button', { name: /open menu/i }).first().click().catch(() => {})
await shot('12-nav', null, { wait: 800 })

// Wall display at TV size
await page.setViewportSize({ width: 1920, height: 1080 })
await shot('13-display-tv', '/display', { wait: 3500 })
await page.setViewportSize({ width: 820, height: 1180 })
await shot('14-display-tablet', '/display', { wait: 3000 })
// Mobile
await page.setViewportSize({ width: 390, height: 844 })
await shot('15-mobile-tasks', '/chores')
await shot('16-mobile-create', '/chores/create', { fullPage: true })

// Words that should not appear anywhere now.
const forbidden = ['Recent Activities', 'Send nudge', 'Circle', 'circle', 'NFC', 'Privacy Policy', 'Terms of', 'Priority', 'Labels', 'Points', 'Notifications', 'Require approval', 'Things', 'Adaptive', 'Yearly', 'Donetick', 'Attachment', 'Privacy settings', 'Scheduling']
for (const f of fs.readdirSync(OUT).filter(f => f.endsWith('.txt'))) {
  const txt = fs.readFileSync(`${OUT}/${f}`, 'utf8')
  const hits = forbidden.filter(w => txt.includes(w))
  if (hits.length) log(`TEXT CHECK ${f}: contains ${hits.join(', ')}`)
}
log('console errors:', consoleErrors.length)
consoleErrors.slice(0, 40).forEach(e => log('  ', e.slice(0, 300)))

await browser.close()
fs.writeFileSync(`${OUT}/report.txt`, report.join('\n'))
