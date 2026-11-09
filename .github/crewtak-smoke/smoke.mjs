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
  // Written as we go so a crash still leaves a readable report.
  fs.appendFileSync(`${OUT}/report.txt`, line + '\n')
}
fs.writeFileSync(`${OUT}/report.txt`, '')
const crashed = e => {
  log('SMOKE CRASHED:', e?.stack || String(e))
  process.exit(1)
}
process.on('uncaughtException', crashed)
process.on('unhandledRejection', crashed)

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

// --- crew users: plain usernames, admin-role management, signup closed ----
{
  const rawStatus = async (path, opts = {}, tok) => {
    const res = await fetch(`${BASE}/api/v1${path}`, {
      ...opts,
      headers: { 'Content-Type': 'application/json', ...(tok ? { Authorization: `Bearer ${tok}` } : {}) },
    })
    return res.status
  }
  const signup2 = await rawStatus('/auth/', { method: 'POST', body: JSON.stringify({ username: 'stranger', password: 'stranger-pass-1', email: 'stranger@example.com' }) })
  log('SIGNUP CLOSED:', signup2 === 403 ? 'OK' : `FAIL (${signup2})`)

  const made = await api('/users/subaccounts', { method: 'POST', body: JSON.stringify({ childName: 'smokemember', displayName: 'Smoke Member', password: 'member-pass-1' }) }, token)
  log('CREW USER USERNAME:', made?.res?.username, made?.res?.username === 'smokemember' ? 'OK' : 'FAIL')
  const dupe = await rawStatus('/users/subaccounts', { method: 'POST', body: JSON.stringify({ childName: 'smokemember', password: 'member-pass-1' }) }, token)
  log('CREW USER DUPLICATE REJECTED:', dupe === 409 ? 'OK' : `FAIL (${dupe})`)

  const mLogin = await api('/auth/login', { method: 'POST', body: JSON.stringify({ username: 'SmokeMember', password: 'member-pass-1' }) })
  const mToken = mLogin.access_token || mLogin.token
  log('CREW USER PLAIN LOGIN (any case):', mToken ? 'OK' : 'FAIL')

  const asMember = await rawStatus('/users/subaccounts', {}, mToken)
  log('MEMBER CANNOT MANAGE USERS:', asMember === 403 ? 'OK' : `FAIL (${asMember})`)

  await api('/circles/members/role', { method: 'PUT', body: JSON.stringify({ memberId: made?.res?.id, role: 'admin' }) }, token)
  const listed = await api('/users/subaccounts', {}, mToken)
  log('PROMOTED ADMIN CAN LIST USERS:', Array.isArray(listed?.res) ? 'OK' : 'FAIL')
  const made2 = await api('/users/subaccounts', { method: 'POST', body: JSON.stringify({ childName: 'smokesecond', password: 'second-pass-1' }) }, mToken)
  log('PROMOTED ADMIN CAN ADD USER:', made2?.res?.username === 'smokesecond' ? 'OK' : 'FAIL')
  const reset = await rawStatus('/users/subaccounts/password', { method: 'PUT', body: JSON.stringify({ childUserId: made2?.res?.id, password: 'second-pass-2' }) }, mToken)
  const relog = await api('/auth/login', { method: 'POST', body: JSON.stringify({ username: 'smokesecond', password: 'second-pass-2' }) })
  log('PROMOTED ADMIN CAN RESET PASSWORD:', reset === 200 && (relog.access_token || relog.token) ? 'OK' : `FAIL (${reset})`)
  const del = await rawStatus(`/users/subaccounts/${made2?.res?.id}`, { method: 'DELETE' }, mToken)
  log('PROMOTED ADMIN CAN DELETE USER:', del === 200 ? 'OK' : `FAIL (${del})`)

  // Crew shift (A/B/C/Ops/Chief) shown under each person's name.
  const madeC = await api('/users/subaccounts', { method: 'POST', body: JSON.stringify({ childName: 'smokeshift', displayName: 'Smoke Shift', password: 'shift-pass-1', crewShift: 'c' }) }, token)
  log('CREW SHIFT ON CREATE:', madeC?.res?.crewShift === 'C' ? 'OK' : `FAIL (${JSON.stringify(madeC?.res)})`)
  const setShift = await rawStatus('/users/subaccounts/shift', { method: 'PUT', body: JSON.stringify({ userId: made?.res?.id, crewShift: 'CHIEF' }) }, token)
  const badShift = await rawStatus('/users/subaccounts/shift', { method: 'PUT', body: JSON.stringify({ userId: made?.res?.id, crewShift: 'D' }) }, token)
  const listedShift = ((await api('/users/subaccounts', {}, token))?.res || []).find(u => u.id === made?.res?.id)
  const memberShift = ((await api('/circles/members', {}, token))?.res || []).find(m => m.userId === madeC?.res?.id)
  log('CREW SHIFT SET:', setShift === 200 && listedShift?.crewShift === 'CHIEF' ? 'OK' : `FAIL (${setShift} ${listedShift?.crewShift})`,
    '| bad value rejected:', badShift === 400 ? 'OK' : `FAIL (${badShift})`,
    '| on crew members:', memberShift?.crewShift === 'C' ? 'OK' : `FAIL (${memberShift?.crewShift})`)
  // Move them to B, then check an ordinary member can't change shifts.
  await api('/users/subaccounts/shift', { method: 'PUT', body: JSON.stringify({ userId: madeC?.res?.id, crewShift: 'B' }) }, token)
  const memberCannotSet = await rawStatus('/users/subaccounts/shift', { method: 'PUT', body: JSON.stringify({ userId: madeC?.res?.id, crewShift: 'A' }) }, (await api('/auth/login', { method: 'POST', body: JSON.stringify({ username: 'smokeshift', password: 'shift-pass-1' }) })).access_token)
  log('CREW SHIFT MEMBER CANNOT SET:', memberCannotSet === 403 ? 'OK' : `FAIL (${memberCannotSet})`)
}

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
  const picked = (await firstPerson.locator('.MuiListItemContent-root').innerText()).split('\n')[0].trim()
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
  await page.getByTestId('watch-day-2').getByRole('button', { name: /^1130p/ }).first().click()
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
// AWS events: create (door + start/end), validation, list, delete.
const awsIds = {}
{
  const now = Date.now()
  const iso = ms => new Date(ms).toISOString()
  const add = (door, startMs, endMs) =>
    api('/crewtak/aws', { method: 'POST', body: JSON.stringify({ door, startAt: iso(startMs), endAt: iso(endMs) }) }, token)
  const H = 3600 * 1000
  const M = 60 * 1000
  awsIds.running = (await add('bc_east', now - 10 * M, now + 50 * M))?.res?.id
  awsIds.upcoming = (await add('american', now + 2 * H, now + 3 * H))?.res?.id
  // Earlier this shift (rejected if the shift started less than 90 min ago).
  awsIds.done = (await add('delta', now - 90 * M, now - 30 * M))?.res?.id
  awsIds.overlap = (await add('bc_east', now + 2 * H, now + 2 * H + 45 * M))?.res?.id
  // More than 48 hours out is always a later tour: not on the board yet.
  awsIds.later = (await add('allegiant', now + 50 * H, now + 51 * H))?.res?.id
  const toDelete = (await add('salt_room', now + 4 * H, now + 5 * H))?.res?.id
  const badDoor = await add('front_gate', now + H, now + 2 * H)
  const backwards = await add('tsa', now + 2 * H, now + H)
  const tooLong = await add('tsa', now, now + 25 * H)
  const lastShift = await add('tsa', now - 60 * H, now - 59 * H)
  const del = await api(`/crewtak/aws/${toDelete}`, { method: 'DELETE' }, token)
  const delAgain = await api(`/crewtak/aws/${toDelete}`, { method: 'DELETE' }, token)
  const list = (await api('/crewtak/aws', {}, token))?.res || []
  const doors = list.map(e => e.door).join(',')
  const sorted = list.every((e, i) => i === 0 || new Date(list[i - 1].startAt) <= new Date(e.startAt))
  log('AWS created:', JSON.stringify(awsIds), awsIds.running && awsIds.upcoming && awsIds.overlap && awsIds.later ? 'OK' : 'FAIL',
    awsIds.done ? '' : '(done-earlier event rejected: shift started <90 min ago)')
  log('AWS list:', doors, sorted && !doors.includes('salt_room') && doors.includes('allegiant') ? 'OK' : 'FAIL')
  log('AWS rejects bad door:', JSON.stringify(badDoor).includes('door') ? 'OK' : 'FAIL',
    '| end before start:', JSON.stringify(backwards).includes('after') ? 'OK' : 'FAIL',
    '| >24h:', JSON.stringify(tooLong).includes('24') ? 'OK' : 'FAIL',
    '| ended last shift:', JSON.stringify(lastShift).includes('before this shift') ? 'OK' : 'FAIL')
  log('AWS delete:', del?.res === toDelete ? 'OK' : 'FAIL', '| delete again 404:', JSON.stringify(delAgain).includes('gone') ? 'OK' : 'FAIL')
}
// Server-side METAR.
{
  const m = await api('/crewtak/weather/metar', {}, token)
  log('METAR API:', JSON.stringify(m).slice(0, 220), /^(SPECI )?KATW /.test(m?.res?.raw || '') ? 'OK' : 'FAIL')
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
await shot('08d-crew-users', '/settings/subaccounts', { fullPage: true })
await shot('09-activities', '/activities', { fullPage: true })
await shot('10-hidden-route-points', '/points')
await shot('11-hidden-route-things', '/things')
await shot('11b-training', '/training')
// Nav drawer
await page.goto(`${BASE}/tasks`)
await page.waitForTimeout(2000)
await page.getByRole('button', { name: /open menu/i }).first().click().catch(() => {})
await shot('12-nav', null, { wait: 800 })

// AWS page: add an event through the form (door, date, start/end, live
// duration), then delete it with the two-tap delete.
{
  const chi = ms => {
    const parts = Object.fromEntries(
      new Intl.DateTimeFormat('en-CA', { timeZone: 'America/Chicago', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
        .formatToParts(new Date(ms)).map(p => [p.type, p.value]),
    )
    return { date: `${parts.year}-${parts.month}-${parts.day}`, hhmm: `${parts.hour}${parts.minute}` }
  }
  const s0 = chi(Date.now() + 30 * 60000)
  const e0 = chi(Date.now() + 75 * 60000)
  try {
    await page.setViewportSize({ width: 1440, height: 900 })
    await shot('19-aws', '/aws', { wait: 2500 })
    const navHasAws = await page.getByRole('link', { name: /^AWS$/ }).count()
    log('AWS in side nav:', navHasAws > 0 ? 'OK' : 'FAIL')
    await page.getByRole('button', { name: /add aws event/i }).click()
    await page.waitForTimeout(400)
    const form = page.getByTestId('aws-add-line')
    await form.locator('[role=combobox]').first().click()
    await page.waitForTimeout(300)
    await page.getByRole('option', { name: 'Basement' }).click()
    await form.locator('input[type=date]').fill(s0.date)
    await form.getByPlaceholder('1615').nth(0).fill(s0.hhmm)
    await form.getByPlaceholder('1615').nth(1).fill(e0.hhmm)
    await page.waitForTimeout(300)
    const dur = (await page.getByTestId('aws-duration').innerText()).trim()
    log('AWS form duration:', dur, dur === '45m' ? 'OK' : 'FAIL')
    await page.screenshot({ path: `${OUT}/19b-aws-add-line.png` })
    await page.getByRole('button', { name: /^add event$/i }).click()
    await page.waitForTimeout(1500)
    const added = await page.locator('[data-testid^=aws-line-]').filter({ hasText: 'Basement' }).count()
    log('AWS added through form:', added === 1 ? 'OK' : 'FAIL')
    await shot('19c-aws-list', null, { wait: 300 })
    const line = page.locator('[data-testid^=aws-line-]').filter({ hasText: 'Basement' }).first()
    await line.getByRole('button', { name: /delete basement/i }).click()
    await page.waitForTimeout(300)
    await line.getByRole('button', { name: /^delete$/ }).click()
    await page.waitForTimeout(1500)
    await page.reload()
    await page.waitForTimeout(2000)
    const left = await page.locator('[data-testid^=aws-line-]').filter({ hasText: 'Basement' }).count()
    log('AWS deleted through page:', left === 0 ? 'OK' : 'FAIL')
    const struck = await page.locator('[data-testid^=aws-line-][data-status=done]').count()
    const later = await page.getByText('Later shifts').count()
    log('AWS page shows done/later:', `done=${struck}`, later ? 'later=OK' : 'later=FAIL')
  } catch (e) {
    log('AWS page flow failed:', e.message)
  }
}
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
  // Weather wording must not be cut off: every line fits its box.
  const cut = await page.getByTestId('weather-widget').evaluate(el =>
    [...el.querySelectorAll('p, span')].filter(n => n.scrollWidth > n.clientWidth + 1 || n.scrollHeight > n.clientHeight + 1).map(n => n.textContent),
  ).catch(e => [`error: ${e.message}`])
  const boardText = await page.evaluate(() => document.body.innerText)
  log('DISPLAY TASK COLUMNS HIDDEN:', !/OVERDUE|DUE TODAY|DONE THIS SHIFT/i.test(boardText) ? 'OK' : 'FAIL')
  const dinner = (await page.getByTestId('display-dinner').innerText().catch(() => '')).replace(/\s+/g, ' ')
  log('DISPLAY DINNER:', dinner, /DAY 1.*Tacos, rice and beans.*DAY 2/i.test(dinner) ? 'OK' : 'FAIL')
  const aws = (await page.getByTestId('display-aws').innerText().catch(() => '')).replace(/\s+/g, ' ')
  const statusOf = async id => (id ? await page.getByTestId(`display-aws-${id}`).getAttribute('data-status').catch(() => null) : 'skipped')
  const st = { running: await statusOf(awsIds.running), upcoming: await statusOf(awsIds.upcoming), done: await statusOf(awsIds.done) }
  const laterShown = await page.getByTestId(`display-aws-${awsIds.later}`).count()
  log('DISPLAY AWS:', aws, JSON.stringify(st),
    st.running === 'active' && st.upcoming === 'upcoming' && (st.done === 'done' || st.done === 'skipped') ? 'OK' : 'FAIL',
    '| later tour hidden:', laterShown === 0 && !/Allegiant/.test(aws) ? 'OK' : 'FAIL',
    '| not editable:', (await page.getByTestId('display-aws').locator('[role=button]').count()) === 0 ? 'OK' : 'FAIL')
  if (awsIds.done) {
    const deco = await page.getByTestId(`display-aws-${awsIds.done}`).locator('p').first().evaluate(el => getComputedStyle(el).textDecorationLine).catch(() => '')
    log('DISPLAY AWS done struck through:', deco, deco.includes('line-through') ? 'OK' : 'FAIL')
  }
  log('DISPLAY NO PENCIL:', (await page.locator('[data-testid=EditRoundedIcon]').count()) === 0 ? 'OK' : 'FAIL')
  log('DISPLAY WEATHER NOT TRUNCATED:', cut.length === 0 ? 'OK' : `FAIL (${cut.join(' | ')})`)
}
// Edit straight from the board: tap FF2 phone, pick someone, check it saved.
try {
  await page.getByTestId('display-slot-phone.ff2').locator('[role=button]').click()
  await page.waitForTimeout(700)
  await page.screenshot({ path: `${OUT}/13b-display-phone-picker.png` })
  const who = (await page.locator('[role=dialog] li [role=button]').filter({ hasNotText: /clear/i }).first().locator('.MuiListItemContent-root').innerText()).split('\n')[0].trim()
  await page.locator('[role=dialog] li [role=button]').filter({ hasNotText: /clear/i }).first().click()
  await page.waitForTimeout(1500)
  const row = (await page.getByTestId('display-slot-phone.ff2').innerText().catch(() => '')).replace(/\s+/g, ' ')
  log('DISPLAY EDIT PHONE:', row, '| picked:', who, row.includes(who) ? 'OK' : 'FAIL')
  await page.getByTestId('display-slot-company.5184').locator('[role=button]').click()
  await page.waitForTimeout(700)
  await page.locator('[role=dialog] li [role=button]').filter({ hasNotText: /done/i }).first().click()
  await page.waitForTimeout(800)
  await page.screenshot({ path: `${OUT}/13c-display-rig-picker.png` })
  await page.getByRole('button', { name: /^done$/i }).first().click()
  await page.waitForTimeout(800)
  await page.reload()
  await page.waitForTimeout(4000)
  const ff2 = (await page.getByTestId('display-slot-phone.ff2').innerText().catch(() => '')).replace(/\s+/g, ' ')
  const rig = (await page.getByTestId('display-slot-company.5184').innerText().catch(() => '')).replace(/\s+/g, ' ')
  log('DISPLAY EDIT SAVED (after reload):', ff2, '|', rig, ff2.includes(who) && !/—/.test(rig) ? 'OK' : 'FAIL')
  await shot('13d-display-after-edit', null, { wait: 500 })
} catch (e) {
  log('DISPLAY EDIT flow failed:', e.message)
}
// Dinner from the board: Day 2 cooks and description.
try {
  await page.getByTestId('display-dinner-2-cooks').click()
  await page.waitForTimeout(600)
  await page.locator('[role=dialog] li [role=button]').filter({ hasNotText: /done/i }).first().click()
  await page.waitForTimeout(600)
  await page.getByRole('button', { name: /^done$/i }).first().click()
  await page.waitForTimeout(600)
  await page.getByTestId('display-dinner-2-note').click()
  await page.waitForTimeout(600)
  await page.locator('[role=dialog] input').fill('Burgers and fries')
  await page.screenshot({ path: `${OUT}/13e-display-dinner-note.png` })
  await page.getByRole('button', { name: /^save$/i }).click()
  await page.waitForTimeout(1500)
  await page.reload()
  await page.waitForTimeout(4000)
  const cooks2 = (await page.getByTestId('display-dinner-2-cooks').innerText().catch(() => '')).trim()
  const note2 = (await page.getByTestId('display-dinner-2-note').innerText().catch(() => '')).trim()
  log('DISPLAY EDIT DINNER (after reload):', cooks2, '|', note2, cooks2 !== '—' && note2 === 'Burgers and fries' ? 'OK' : 'FAIL')
  await shot('13f-display-final', null, { wait: 500 })
} catch (e) {
  log('DISPLAY DINNER flow failed:', e.message)
}
await page.setViewportSize({ width: 820, height: 1180 })
await shot('14-display-tablet', '/display', { wait: 3000 })
// Mobile
await page.setViewportSize({ width: 390, height: 844 })
await shot('15-mobile-tasks', '/tasks')
await shot('16-mobile-create', '/tasks/create', { fullPage: true })
await shot('17-mobile-shift-info', '/shift', { wait: 2500 })
await shot('17b-mobile-aws', '/aws', { wait: 2500 })

// Words that should not appear anywhere now.
const forbidden = ['Sub Account', 'Sub account', 'Primary Account', 'Managed Accounts', 'Recent Activities', 'Send nudge', 'Circle', 'circle', 'NFC', 'Privacy Policy', 'Terms of', 'Priority', 'Labels', 'Points', 'Notifications', 'Require approval', 'Things', 'Adaptive', 'Yearly', 'Donetick', 'Attachment', 'Privacy settings', 'Scheduling']
for (const f of fs.readdirSync(OUT).filter(f => f.endsWith('.txt'))) {
  const txt = fs.readFileSync(`${OUT}/${f}`, 'utf8')
  const hits = forbidden.filter(w => txt.includes(w))
  if (hits.length) log(`TEXT CHECK ${f}: contains ${hits.join(', ')}`)
}
log('console errors:', consoleErrors.length)
consoleErrors.slice(0, 40).forEach(e => log('  ', e.slice(0, 300)))

await browser.close()

