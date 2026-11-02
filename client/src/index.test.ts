import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  WebPushError,
  currentSubscription,
  isIOS,
  isPushSupported,
  isStandalone,
  needsHomeScreenInstall,
  permission,
  pushStatus,
  requestPermission,
  subscribe,
  unsubscribe,
  urlBase64ToUint8Array,
} from './index'

const KEY = 'BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM'

interface Env {
  ua?: string
  maxTouchPoints?: number
  standalone?: boolean
  displayStandalone?: boolean
  perm?: NotificationPermission
  answer?: NotificationPermission
  noPush?: boolean
  noRegistration?: boolean
  existing?: { key?: Uint8Array }
  subscribeFails?: boolean
  fetchStatus?: number
}

function fakeSubscription(endpoint: string, key?: Uint8Array) {
  return {
    endpoint,
    options: { applicationServerKey: key ? key.buffer : null },
    toJSON: () => ({ endpoint, keys: { p256dh: 'P', auth: 'A' } }),
    unsubscribe: vi.fn(async () => true),
  }
}

function setup(env: Env = {}) {
  let sub: ReturnType<typeof fakeSubscription> | null = env.existing ? fakeSubscription('https://push.example.com/old', env.existing.key) : null
  const pushManager = {
    getSubscription: vi.fn(async () => sub),
    subscribe: vi.fn(async (o: { applicationServerKey: Uint8Array }) => {
      if (env.subscribeFails) throw new Error('push service not available')
      sub = fakeSubscription('https://push.example.com/new', o.applicationServerKey)
      return sub
    }),
  }
  const reg = { active: {}, pushManager }
  const serviceWorker = {
    getRegistration: vi.fn(async () => (env.noRegistration ? undefined : reg)),
    register: vi.fn(async () => reg),
    ready: Promise.resolve(reg),
  }
  const Notification = {
    permission: env.perm ?? 'default',
    requestPermission: vi.fn(async () => {
      Notification.permission = env.answer ?? 'granted'
      return Notification.permission
    }),
  }
  const win: Record<string, unknown> = {
    Notification,
    matchMedia: (q: string) => ({ matches: q.includes('standalone') && Boolean(env.displayStandalone) }),
  }
  if (!env.noPush) win.PushManager = function PushManager() {}
  vi.stubGlobal('window', win)
  vi.stubGlobal('Notification', Notification)
  const nav: Record<string, unknown> = {
    userAgent: env.ua ?? 'Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0',
    maxTouchPoints: env.maxTouchPoints ?? 0,
    language: 'en-US',
    standalone: env.standalone,
  }
  if (!env.noPush) nav.serviceWorker = serviceWorker
  vi.stubGlobal('navigator', nav)
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    void init
    if (url.endsWith('vapid-public-key')) return new Response(JSON.stringify({ publicKey: KEY }), { status: 200 })
    return new Response(null, { status: env.fetchStatus ?? 204 })
  })
  vi.stubGlobal('fetch', fetchMock)
  return { pushManager, serviceWorker, Notification, fetchMock, getSub: () => sub }
}

const IPHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Safari/604.1'
const IPAD = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15'

beforeEach(() => vi.unstubAllGlobals())
afterEach(() => vi.unstubAllGlobals())

describe('feature checks', () => {
  it('reports support', () => {
    setup()
    expect(isPushSupported()).toBe(true)
    setup({ noPush: true })
    expect(isPushSupported()).toBe(false)
  })

  it('detects iOS, including iPadOS that says it is a Mac', () => {
    setup({ ua: IPHONE })
    expect(isIOS()).toBe(true)
    setup({ ua: IPAD, maxTouchPoints: 5 })
    expect(isIOS()).toBe(true)
    setup({ ua: IPAD, maxTouchPoints: 0 })
    expect(isIOS()).toBe(false)
  })

  it('needs the Home Screen on iOS until opened as an installed app', () => {
    setup({ ua: IPHONE, noPush: true })
    expect(needsHomeScreenInstall()).toBe(true)
    setup({ ua: IPHONE, standalone: true })
    expect(isStandalone()).toBe(true)
    expect(needsHomeScreenInstall()).toBe(false)
    setup({ displayStandalone: true })
    expect(isStandalone()).toBe(true)
    setup()
    expect(needsHomeScreenInstall()).toBe(false)
  })

  it('reads and requests permission', async () => {
    const env = setup({ perm: 'default', answer: 'granted' })
    expect(permission()).toBe('default')
    expect(await requestPermission()).toBe('granted')
    expect(env.Notification.requestPermission).toHaveBeenCalledOnce()
    // Already decided: no prompt.
    expect(await requestPermission()).toBe('granted')
    expect(env.Notification.requestPermission).toHaveBeenCalledOnce()
  })

  it('decodes base64url keys', () => {
    const bytes = urlBase64ToUint8Array(KEY)
    expect(bytes.length).toBe(65)
    expect(bytes[0]).toBe(4)
    expect(Array.from(urlBase64ToUint8Array('_-8'))).toEqual([0xff, 0xef])
  })
})

describe('subscribe', () => {
  it('asks permission, subscribes with the server key and saves the subscription', async () => {
    const env = setup()
    const sub = await subscribe({ lang: 'he', fetchInit: { headers: { 'X-CSRF': 't' } } })

    expect(sub.endpoint).toBe('https://push.example.com/new')
    const call = env.pushManager.subscribe.mock.calls[0][0] as { userVisibleOnly: boolean; applicationServerKey: Uint8Array }
    expect(call.userVisibleOnly).toBe(true)
    expect(call.applicationServerKey.length).toBe(65)

    const [url, init] = env.fetchMock.mock.calls[1]
    expect(url).toBe('/api/push/subscriptions')
    expect(init?.method).toBe('POST')
    expect(init?.credentials).toBe('same-origin')
    expect(init?.headers).toEqual({ 'X-CSRF': 't', 'Content-Type': 'application/json' })
    expect(JSON.parse(String(init?.body))).toEqual({ endpoint: 'https://push.example.com/new', keys: { p256dh: 'P', auth: 'A' }, lang: 'he' })
  })

  it('uses a given key and URLs without fetching the key', async () => {
    const env = setup()
    await subscribe({ publicKey: KEY, subscriptionsUrl: '/push/subs' })
    expect(env.fetchMock).toHaveBeenCalledOnce()
    expect(env.fetchMock.mock.calls[0][0]).toBe('/push/subs')
  })

  it('reuses a subscription made with the same key, replaces one made with another', async () => {
    let env = setup({ existing: { key: urlBase64ToUint8Array(KEY) } })
    let sub = await subscribe()
    expect(sub.endpoint).toBe('https://push.example.com/old')
    expect(env.pushManager.subscribe).not.toHaveBeenCalled()

    env = setup({ existing: { key: new Uint8Array(65) } })
    const old = env.getSub()!
    sub = await subscribe()
    expect(old.unsubscribe).toHaveBeenCalled()
    expect(sub.endpoint).toBe('https://push.example.com/new')
  })

  it('registers the given service worker when the page has none', async () => {
    const env = setup({ noRegistration: true })
    await subscribe({ serviceWorkerUrl: '/push-sw.js' })
    expect(env.serviceWorker.register).toHaveBeenCalledWith('/push-sw.js', undefined)
  })

  const failures: [string, Env, Parameters<typeof subscribe>[0], string][] = [
    ['unsupported', { noPush: true }, {}, 'unsupported'],
    ['iOS in Safari', { noPush: true, ua: IPHONE }, {}, 'needs-home-screen'],
    ['blocked', { answer: 'denied' }, {}, 'permission-denied'],
    ['prompt closed', { answer: 'default' }, {}, 'permission-dismissed'],
    ['no service worker', { noRegistration: true }, {}, 'no-service-worker'],
    ['push service refuses', { subscribeFails: true }, {}, 'subscribe-failed'],
    ['server refuses', { fetchStatus: 401 }, { publicKey: KEY }, 'server'],
  ]
  for (const [name, env, opts, code] of failures) {
    it(`rejects with code ${code} when ${name}`, async () => {
      setup(env)
      const err = await subscribe(opts).catch((e: unknown) => e)
      expect(err).toBeInstanceOf(WebPushError)
      expect((err as WebPushError).code).toBe(code)
    })
  }

  it('reports the status of a server error', async () => {
    setup({ fetchStatus: 500 })
    const err = (await subscribe({ publicKey: KEY }).catch((e: unknown) => e)) as WebPushError
    expect(err.status).toBe(500)
  })
})

describe('unsubscribe and status', () => {
  it('tells the server and unsubscribes the browser', async () => {
    const env = setup({ existing: {} })
    const old = env.getSub()!
    expect(await unsubscribe()).toBe(true)
    const [url, init] = env.fetchMock.mock.calls[0]
    expect(url).toBe('/api/push/subscriptions')
    expect(init?.method).toBe('DELETE')
    expect(JSON.parse(String(init?.body))).toEqual({ endpoint: 'https://push.example.com/old' })
    expect(old.unsubscribe).toHaveBeenCalled()
  })

  it('unsubscribes the browser even when the server fails', async () => {
    const env = setup({ existing: {}, fetchStatus: 500 })
    const old = env.getSub()!
    await expect(unsubscribe()).rejects.toMatchObject({ code: 'server', status: 500 })
    expect(old.unsubscribe).toHaveBeenCalled()
  })

  it('resolves false with nothing to remove', async () => {
    const env = setup()
    expect(await unsubscribe()).toBe(false)
    expect(env.fetchMock).not.toHaveBeenCalled()
    expect(await currentSubscription()).toBeNull()
  })

  it('says what the UI should offer', async () => {
    setup({ noPush: true })
    expect(await pushStatus()).toBe('unsupported')
    setup({ noPush: true, ua: IPHONE })
    expect(await pushStatus()).toBe('needs-home-screen')
    setup({ perm: 'denied' })
    expect(await pushStatus()).toBe('denied')
    setup({ perm: 'default' })
    expect(await pushStatus()).toBe('not-subscribed')
    setup({ perm: 'granted', existing: {} })
    expect(await pushStatus()).toBe('subscribed')
  })
})
