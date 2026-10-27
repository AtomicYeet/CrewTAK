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
  ['Early AM Perimeter', 'daily', at(-6, 6, 30)],
  ['Old one-off chore', 'once', at(-2, 12, 0)],
]) {
  const r = await chore(name, type, due)
  ids.push(r?.res ?? r?.id ?? r)
}
log('created chores:', JSON.stringify(ids))
// A project with its own task, for the "All Projects" view.
const proj = await api('/projects', { method: 'POST', body: JSON.stringify({ name: 'Monthly Fire Extinguishers', color: '#d32f2f', icon: 'Build' }) }, token)
const projId = proj?.res?.id ?? proj?.id
log('project created:', projId)
if (projId) await chore('Extinguisher Check Hangar', 'weekly', at(2, 23, 59), { projectId: projId })
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

// Undo: complete a task, then undo it straight away.
const undoTarget = (choresList.res || []).find(c => c.name.startsWith('Take out Station'))
if (undoTarget) {
  await api(`/chores/${undoTarget.id}/do`, { method: 'POST', body: JSON.stringify({}) }, token)
  const undo = await api(`/chores/${undoTarget.id}/undo`, { method: 'POST', body: '{}' }, token)
  const after = await api(`/chores/${undoTarget.id}`, {}, token)
  log('UNDO result:', JSON.stringify(undo).slice(0, 120), '| due after undo:', after?.res?.nextDueDate, '| original due:', undoTarget.nextDueDate)
}

// Profile photo upload (1x1 PNG).
{
  const png = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
    'base64',
  )
  const fd = new FormData()
  fd.append('file', new Blob([png], { type: 'image/png' }), 'photo.PNG')
  const res = await fetch(`${BASE}/api/v1/users/profile_photo`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
    body: fd,
  })
  const body = await res.text()
  log('PHOTO upload:', res.status, body.slice(0, 160))
  const sign = (() => {
    try {
      return JSON.parse(body).sign
    } catch {
      return null
    }
  })()
  if (sign) {
    const url = sign.startsWith('http') ? sign : `${BASE}/api/v1/assets/${sign.replace(/^\/+/, '')}`
    const img = await fetch(url)
    log('PHOTO fetch:', img.status, img.headers.get('content-type'), url.replace(/sig=[^&]+/, 'sig=…'))
  }
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
await shot('01b-home-quick-add', null, {
  before: async () => {
    await page.getByText(/add a task/i).first().click()
    await page.waitForTimeout(1000)
  },
  wait: 300,
})
await page.keyboard.press('Escape')
await page.waitForTimeout(500)
await shot('02-tasks', '/tasks')
// Old /chores links must land on /tasks.
await page.goto(`${BASE}/chores?project=default`)
await page.waitForTimeout(1500)
log('REDIRECT /chores ->', new URL(page.url()).pathname + new URL(page.url()).search)
// Future repeats on the timeline (on by default, 2 weeks).
await shot('02b-tasks-repeats', '/tasks', { fullPage: true, wait: 3000 })
{
  const t = fs.readFileSync(`${OUT}/02b-tasks-repeats.txt`, 'utf8')
  const n = (t.match(/Daily Truck Inspections/g) || []).length
  log('REPEATS daily truck occurrences on /tasks:', n, n > 3 ? 'OK' : 'TOO FEW')
}
// All Projects overview.
await shot('02c-tasks-all-projects', '/tasks?project=all', { fullPage: true, wait: 3000 })
{
  const t = fs.readFileSync(`${OUT}/02c-tasks-all-projects.txt`, 'utf8')
  log('ALL PROJECTS shows project task:', t.includes('Extinguisher Check Hangar') ? 'OK' : 'MISSING', '| shows default task:', t.includes('Water Softener') ? 'OK' : 'MISSING')
}
await shot('02d-tasks-calendar', null, {
  before: async () => {
    await page.evaluate(() => localStorage.setItem('choreCardViewMode', 'calendar'))
    await page.reload()
    await page.waitForTimeout(2500)
  },
  fullPage: true,
  wait: 300,
})
await page.evaluate(() => localStorage.setItem('choreCardViewMode', 'default'))
await shot('03-create', '/tasks/create', { fullPage: true })
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
await shot('05-quick-add', '/tasks?add_task=1', { wait: 3000 })
await shot('06-task-view', first ? `/tasks/${first.id}` : '/tasks')
await shot('06b-task-menu', null, {
  before: async () => {
    await page.getByRole('button', { name: /^more$/i }).first().click()
    await page.waitForTimeout(600)
  },
  wait: 200,
})
await shot('07-log', '/log')
await shot('07b-log-entry', null, {
  before: async () => {
    await page.locator('tbody tr').filter({ hasText: 'Daily Truck' }).first().click()
    await page.waitForTimeout(700)
  },
  wait: 200,
})
await page.keyboard.press('Escape')
// Shift Info: assign the Captain's phone and the first sleep block, then
// reload to prove the picks were saved on the server.
await shot('07c-shift-info-empty', '/shift', { wait: 2500 })
await shot('07d-shift-info-picker', null, {
  before: async () => {
    await page.getByRole('button', { name: /captain's/i }).first().click()
    await page.waitForTimeout(600)
  },
  wait: 200,
})
try {
  const firstPerson = page.locator('[role=dialog] li [role=button]').filter({ hasNotText: /clear/i }).first()
  const picked = (await firstPerson.textContent())?.trim()
  await firstPerson.click()
  await page.waitForTimeout(800)
  await page.getByRole('button', { name: /8p/ }).first().click()
  await page.waitForTimeout(600)
  await page.locator('[role=dialog] li [role=button]').filter({ hasNotText: /clear/i }).first().click()
  await page.waitForTimeout(1200)
  await page.reload()
  await page.waitForTimeout(2500)
  const captainBox = (await page.getByRole('button', { name: /captain's/i }).first().textContent()) || ''
  log('SHIFT INFO captain after reload:', JSON.stringify(captainBox), '| expected to contain:', JSON.stringify(picked), picked && captainBox.includes(picked) ? 'OK' : 'MISMATCH')
} catch (e) {
  log('SHIFT INFO flow failed:', e.message)
}
// Put the first crew member on 5181 through the multi-select rig picker.
try {
  await page.goto(`${BASE}/shift`)
  await page.waitForTimeout(2000)
  await page.getByRole('button', { name: /5181 Crash Truck/ }).first().click()
  await page.waitForTimeout(600)
  await page.locator('[role=dialog] li [role=button]').filter({ hasNotText: /done/i }).first().click()
  await page.waitForTimeout(1000)
  await page.screenshot({ path: `${OUT}/07d2-rig-picker.png` })
  await page.getByRole('button', { name: /^done$/i }).first().click()
  await page.waitForTimeout(800)
} catch (e) {
  log('RIG picker flow failed:', e.message)
}
// Day 2 watch time and Day 1 dinner (cooks + description) through the UI.
try {
  await page.goto(`${BASE}/shift`)
  await page.waitForTimeout(2000)
  await page.getByTestId('watch-day-2').getByRole('button', { name: /1130p/ }).first().click()
  await page.waitForTimeout(600)
  await page.locator('[role=dialog] li [role=button]').filter({ hasNotText: /clear/i }).first().click()
  await page.waitForTimeout(1000)
  await page.getByTestId('dinner-day-1').getByRole('button', { name: /cooking/i }).first().click()
  await page.waitForTimeout(600)
  await page.locator('[role=dialog] li [role=button]').filter({ hasNotText: /done/i }).first().click()
  await page.waitForTimeout(800)
  await page.getByRole('button', { name: /^done$/i }).first().click()
  await page.waitForTimeout(600)
  const note = page.getByLabel('Day 1 dinner description')
  await note.fill('Tacos, rice and beans')
  await note.press('Enter')
  await page.waitForTimeout(1500)
  await page.reload()
  await page.waitForTimeout(2500)
  const watch2 = (await page.getByTestId('watch-day-2').innerText()).replace(/\s+/g, ' ')
  const dinner1 = (await page.getByTestId('dinner-day-1').innerText()).replace(/\s+/g, ' ')
  const noteVal = await page.getByLabel('Day 1 dinner description').inputValue()
  log('WATCH DAY 2 after reload:', watch2, /1130p – 3a → Crew Test/.test(watch2) ? 'OK' : 'CHECK')
  log('DINNER DAY 1 after reload:', dinner1, '| note:', JSON.stringify(noteVal), noteVal === 'Tacos, rice and beans' && dinner1.includes('Crew Test') ? 'OK' : 'FAIL')
} catch (e) {
  log('WATCH/DINNER flow failed:', e.message)
}
await shot('07e-shift-info-filled', '/shift', { wait: 2500, fullPage: true })
// One phone per person: put the same person on FF1, then Ops; FF1 must empty.
{
  const me = await api('/users/profile', {}, token)
  const uid = me?.res?.id
  if (uid) {
    const T = '2030-01-01'
    await api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: T, slot: 'phone.ff1', userId: uid }) }, token)
    const r = await api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: T, slot: 'phone.ops', userId: uid }) }, token)
    const after = await api(`/crewtak/shift-info?tour=${T}`, {}, token)
    const map = Object.fromEntries((after.res || []).map(x => [x.slot, x.userId]))
    await api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: T, slot: 'sleep.1.0', userId: uid }) }, token)
    await api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: T, slot: 'sleep.1.2', userId: uid }) }, token)
    await api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: T, slot: 'sleep.2.1', userId: uid }) }, token)
    await api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: T, slot: `company.5181.u${uid}`, userId: uid }) }, token)
    await api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: T, slot: `company.5132.u${uid}`, userId: uid }) }, token)
    const wrongUser = await api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: T, slot: `company.5184.u${uid + 999}`, userId: uid }) }, token)
    log('RIG slot/user mismatch rejected:', JSON.stringify(wrongUser).includes('Unknown') ? 'OK' : 'FAIL')
    const after2 = await api(`/crewtak/shift-info?tour=${T}`, {}, token)
    const m2 = Object.fromEntries((after2.res || []).map(x => [x.slot, x.userId]))
    const ok2 = m2['sleep.1.0'] == null && m2['sleep.1.2'] === uid && m2['sleep.2.1'] === uid && m2[`company.5181.u${uid}`] === uid && m2[`company.5132.u${uid}`] === uid && m2['phone.ops'] === uid
    log('WATCH one-per-person, RIGS many-to-many:', JSON.stringify(m2), ok2 ? 'OK' : 'FAIL')
    log('PHONES one-per-person:', JSON.stringify(map), '| cleared:', JSON.stringify(r?.cleared), map['phone.ops'] === uid && map['phone.ff1'] == null ? 'OK' : 'FAIL')
  }
}
// Dinner: many cooks per day, a short description, 255-character limit.
{
  const me = await api('/users/profile', {}, token)
  const uid = me?.res?.id
  const T = '2030-02-01'
  const put = body => api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: T, ...body }) }, token)
  await put({ slot: `dinner.2.u${uid}`, userId: uid })
  await put({ slot: 'dinner.2.note', note: '  Chili night  ' })
  const tooLong = await put({ slot: 'dinner.2.note', note: 'x'.repeat(256) })
  const noteWithUser = await put({ slot: 'dinner.2.note', userId: uid, note: 'x' })
  const got = await api(`/crewtak/shift-info?tour=${T}`, {}, token)
  const rows = Object.fromEntries((got.res || []).map(x => [x.slot, x]))
  const ok = rows[`dinner.2.u${uid}`]?.userId === uid && rows['dinner.2.note']?.note === 'Chili night'
  log('DINNER API:', JSON.stringify(rows['dinner.2.note']), ok ? 'OK' : 'FAIL',
    '| 256 chars rejected:', JSON.stringify(tooLong).includes('255') ? 'OK' : 'FAIL',
    '| note+user rejected:', JSON.stringify(noteWithUser).includes('Unknown') ? 'OK' : 'FAIL')
}
// Server-side METAR.
{
  const m = await api('/crewtak/weather/metar', {}, token)
  log('METAR API:', JSON.stringify(m).slice(0, 220), m?.res?.raw?.startsWith('KATW') ? 'OK' : 'FAIL')
}
const badSlot = await api('/crewtak/shift-info', { method: 'PUT', body: JSON.stringify({ tour: '2026-01-01', slot: 'nope', userId: null }) }, token)
log('SHIFT INFO bad slot rejected:', JSON.stringify(badSlot).slice(0, 80))
// Clear all overdue from Home.
{
  const before = (await api('/chores/', {}, token)).res || []
  const overdueBefore = before.filter(c => c.nextDueDate && new Date(c.nextDueDate) < new Date())
  log('CLEAR OVERDUE before:', overdueBefore.map(c => c.name).join(' | '))
  await page.setViewportSize({ width: 1440, height: 900 })
  await shot('18-home-clear-button', '/home', { wait: 2500 })
  try {
    await page.getByRole('button', { name: /clear all overdue tasks/i }).first().click()
    await page.waitForTimeout(700)
    await page.screenshot({ path: `${OUT}/18b-clear-confirm.png` })
    await page.getByRole('button', { name: /skip them all/i }).first().click()
    await page.waitForTimeout(3000)
    await page.screenshot({ path: `${OUT}/18c-home-after-clear.png` })
  } catch (e) {
    log('CLEAR OVERDUE UI failed:', e.message)
  }
  const after = (await api('/chores/', {}, token)).res || []
  const overdueAfter = after.filter(c => c.nextDueDate && new Date(c.nextDueDate) < new Date())
  const perim = after.find(c => c.name === 'Early AM Perimeter')
  log('CLEAR OVERDUE after:', overdueAfter.length === 0 ? 'none OK' : overdueAfter.map(c => c.name).join(' | '), '| perimeter next due:', perim?.nextDueDate, '| one-off still active:', Boolean(after.find(c => c.name === 'Old one-off chore')))
  if (perim) {
    const h = await api(`/chores/${perim.id}/history`, {}, token)
    const rows = (h.res || []).map(x => `status=${x.status} by=${x.completedBy}`)
    log('CLEAR OVERDUE perimeter history:', rows.join(', '))
  }
}
await shot('08-settings', '/settings', { fullPage: true })
await shot('08b-crew-settings', '/settings/circle', { fullPage: true })
await shot('08c-profile', '/settings/profile', { fullPage: true })
await shot('09-activities', '/activities', { fullPage: true })
await shot('10-hidden-route-points', '/points')
await shot('11-hidden-route-things', '/things')
await shot('11b-training', '/training')
// Nav drawer
await page.goto(`${BASE}/tasks`)
await page.waitForTimeout(2000)
await page.getByRole('button', { name: /open menu/i }).first().click().catch(() => {})
await shot('12-nav', null, { wait: 800 })

// Wall display at TV size
await page.setViewportSize({ width: 1920, height: 1080 })
await shot('13-display-tv', '/display', { wait: 6000 })
{
  const clock = (await page.getByTestId('display-clock').innerText().catch(() => '')).replace(/\s+/g, ' ')
  log('DISPLAY CLOCK:', clock, /\d\d:\d\d ?\| ?\d\d:\d\d ?Z/.test(clock) ? 'OK' : 'FAIL')
  const weather = (await page.getByTestId('weather-widget').innerText().catch(() => '')).replace(/\s+/g, ' ')
  log('DISPLAY WEATHER:', weather || '(missing)')
  const metar = (await page.getByTestId('metar-strip').innerText().catch(() => '')).replace(/\s+/g, ' ')
  log('DISPLAY METAR:', metar || '(none shown)')
  const watch = (await page.getByTestId('display-watch').innerText().catch(() => '')).replace(/\s+/g, ' ')
  log('DISPLAY WATCH:', watch, /DAY 1.*DAY 2/i.test(watch) ? 'OK' : 'FAIL')
  const logo = await page.locator('img[src*="logo"]').count()
  log('DISPLAY LOGO REMOVED:', logo === 0 ? 'OK' : 'FAIL')
}
await page.setViewportSize({ width: 820, height: 1180 })
await shot('14-display-tablet', '/display', { wait: 3000 })
// Mobile
await page.setViewportSize({ width: 390, height: 844 })
await shot('15-mobile-tasks', '/tasks')
await shot('16-mobile-create', '/tasks/create', { fullPage: true })
await shot('17-mobile-shift-info', '/shift', { wait: 2500 })

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
