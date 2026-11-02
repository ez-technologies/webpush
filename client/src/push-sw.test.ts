// The classic script built from sw.ts (npm run build) installs itself.
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { expect, it, vi } from 'vitest'

const script = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', 'dist', 'push-sw.js'), 'utf8')

function run(href: string, options?: Record<string, unknown>) {
  const listeners: Record<string, (e: unknown) => void> = {}
  const self = {
    location: { origin: new URL(href).origin, href },
    registration: { showNotification: vi.fn(async () => undefined) },
    clients: { matchAll: vi.fn(async () => []), openWindow: vi.fn(async () => undefined) },
    addEventListener: (type: string, l: (e: unknown) => void) => {
      listeners[type] = l
    },
    WEBPUSH_OPTIONS: options,
  }
  runInNewContext(script, { self, URL })
  return { self, listeners }
}

async function push(listeners: Record<string, (e: unknown) => void>, payload: unknown) {
  const waits: Promise<unknown>[] = []
  listeners.push({ data: { json: () => payload, text: () => '' }, waitUntil: (p: Promise<unknown>) => waits.push(p) })
  await Promise.all(waits)
}

it('installs the push and click handlers', () => {
  const { listeners } = run('https://app.example.com/push-sw.js')
  expect(Object.keys(listeners).sort()).toEqual(['notificationclick', 'push'])
})

it('takes options from the query and from self.WEBPUSH_OPTIONS', async () => {
  const fromQuery = run('https://app.example.com/push-sw.js?title=Demo&icon=/i.png')
  await push(fromQuery.listeners, { body: 'b' })
  expect(fromQuery.self.registration.showNotification).toHaveBeenCalledWith('Demo', expect.objectContaining({ icon: '/i.png' }))

  const fromGlobal = run('https://app.example.com/sw.js?title=Demo', { defaultTitle: 'Global' })
  await push(fromGlobal.listeners, { body: 'b' })
  expect(fromGlobal.self.registration.showNotification).toHaveBeenCalledWith('Global', expect.anything())
})
