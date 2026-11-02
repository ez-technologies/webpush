// Browser side of Web Push: is it supported here, ask permission, subscribe
// this browser and tell your server, unsubscribe.
//
// The server side is the Go module in this repository; the default URLs match
// its Gin handlers (package ginpush) mounted at /api/push.

/** Why a call could not do what was asked. */
export type WebPushErrorCode =
  /** This browser has no service worker, PushManager or Notification API. */
  | 'unsupported'
  /** iPhone or iPad: push only works once the site is added to the Home Screen and opened from there. */
  | 'needs-home-screen'
  /** The user (or a browser setting) blocked notifications for this site. */
  | 'permission-denied'
  /** The user closed the permission prompt without choosing. */
  | 'permission-dismissed'
  /** No service worker is registered (or it did not become ready in time). */
  | 'no-service-worker'
  /** The server has no VAPID public key to give, or answered with an error. */
  | 'server'
  /** The browser's push service refused to subscribe. */
  | 'subscribe-failed'

export class WebPushError extends Error {
  readonly code: WebPushErrorCode
  /** HTTP status, for code 'server'. */
  readonly status?: number

  constructor(code: WebPushErrorCode, message: string, status?: number) {
    super(message)
    this.name = 'WebPushError'
    this.code = code
    this.status = status
  }
}

export interface PushOptions {
  /** The VAPID public key (base64url). When absent it is fetched from publicKeyUrl. */
  publicKey?: string
  /** Answers {"publicKey": "..."}. Default '/api/push/vapid-public-key'. */
  publicKeyUrl?: string
  /** POST saves a subscription, DELETE removes it. Default '/api/push/subscriptions'. */
  subscriptionsUrl?: string
  /**
   * A service worker script to register when the page has none, for example
   * '/push-sw.js'. Without it, the page's existing service worker is used.
   */
  serviceWorkerUrl?: string
  /** Scope for serviceWorkerUrl. Default the browser's (the script's folder). */
  scope?: string
  /** Sent with the subscription so the server can word messages for this device. */
  lang?: string
  /** Merged into every request (headers such as a CSRF token, credentials). Default credentials 'same-origin'. */
  fetchInit?: RequestInit
  /** How long to wait for the service worker to become active. Default 10000 ms. */
  readyTimeoutMs?: number
}

const defaults = {
  publicKeyUrl: '/api/push/vapid-public-key',
  subscriptionsUrl: '/api/push/subscriptions',
  readyTimeoutMs: 10000,
}

/** The browser has everything Web Push needs. */
export function isPushSupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof navigator !== 'undefined' &&
    'serviceWorker' in navigator &&
    'PushManager' in window &&
    'Notification' in window
  )
}

/** iPhone, iPad or iPod (iPadOS reports itself as a Mac with a touch screen). */
export function isIOS(): boolean {
  if (typeof navigator === 'undefined') return false
  const ua = navigator.userAgent || ''
  if (/iPad|iPhone|iPod/.test(ua)) return true
  return /Macintosh/.test(ua) && (navigator.maxTouchPoints ?? 0) > 1
}

/** The page runs as an installed app (opened from the Home Screen or app list). */
export function isStandalone(): boolean {
  if (typeof window === 'undefined') return false
  const nav = navigator as Navigator & { standalone?: boolean }
  if (nav.standalone === true) return true
  return typeof window.matchMedia === 'function' && window.matchMedia('(display-mode: standalone)').matches
}

/**
 * True on iOS when the page is open in Safari rather than as a Home Screen app.
 * iOS (16.4 and later) only offers push to installed web apps: tell the user to
 * use Share, then Add to Home Screen, and open the app from there.
 */
export function needsHomeScreenInstall(): boolean {
  return isIOS() && !isStandalone()
}

/** The notification permission, or 'unsupported'. */
export function permission(): NotificationPermission | 'unsupported' {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported'
  return Notification.permission
}

/**
 * Asks the user for permission (only shows a prompt when it is 'default').
 * Call it from a click or tap: browsers ignore or block prompts that are not.
 */
export async function requestPermission(): Promise<NotificationPermission> {
  if (permission() === 'unsupported') throw new WebPushError('unsupported', 'notifications are not supported in this browser')
  if (Notification.permission !== 'default') return Notification.permission
  return Notification.requestPermission()
}

/** Decodes a base64url VAPID key into the bytes PushManager.subscribe wants. */
export function urlBase64ToUint8Array(value: string): Uint8Array<ArrayBuffer> {
  const base64 = (value + '='.repeat((4 - (value.length % 4)) % 4)).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(base64)
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

function unsupportedReason(): WebPushError | null {
  if (needsHomeScreenInstall() && !isPushSupported()) {
    return new WebPushError('needs-home-screen', 'on iOS, add this site to the Home Screen and open it from there to get notifications')
  }
  if (!isPushSupported()) return new WebPushError('unsupported', 'push notifications are not supported in this browser')
  return null
}

/** The service worker registration that holds the subscription, or null. With wait, registers serviceWorkerUrl when needed and waits until it is active. */
export async function getRegistration(opts: PushOptions = {}, wait = false): Promise<ServiceWorkerRegistration | null> {
  if (!isPushSupported()) return null
  const sw = navigator.serviceWorker
  let reg = (await sw.getRegistration(opts.scope)) ?? null
  if (!reg && opts.serviceWorkerUrl && wait) {
    reg = await sw.register(opts.serviceWorkerUrl, opts.scope ? { scope: opts.scope } : undefined)
  }
  if (!reg || !wait || reg.active) return reg
  const timeout = opts.readyTimeoutMs ?? defaults.readyTimeoutMs
  const ready = await Promise.race([
    sw.ready,
    new Promise<null>((resolve) => setTimeout(() => resolve(null), timeout)),
  ])
  return ready
}

/** This browser's current push subscription, or null. */
export async function currentSubscription(opts: PushOptions = {}): Promise<PushSubscription | null> {
  const reg = await getRegistration(opts)
  return reg ? reg.pushManager.getSubscription() : null
}

export type PushStatus = 'unsupported' | 'needs-home-screen' | 'denied' | 'subscribed' | 'not-subscribed'

/** One word for what the UI should offer: nothing, an install hint, a "blocked" note, "turn off" or "turn on". */
export async function pushStatus(opts: PushOptions = {}): Promise<PushStatus> {
  const reason = unsupportedReason()
  if (reason) return reason.code === 'needs-home-screen' ? 'needs-home-screen' : 'unsupported'
  if (Notification.permission === 'denied') return 'denied'
  const sub = await currentSubscription(opts)
  return sub && Notification.permission === 'granted' ? 'subscribed' : 'not-subscribed'
}

function init(opts: PushOptions, extra: RequestInit): RequestInit {
  const base = opts.fetchInit ?? {}
  return {
    credentials: 'same-origin',
    ...base,
    ...extra,
    headers: { ...(base.headers as Record<string, string> | undefined), ...(extra.headers as Record<string, string> | undefined) },
  }
}

async function fetchPublicKey(opts: PushOptions): Promise<string> {
  if (opts.publicKey) return opts.publicKey
  const res = await fetch(opts.publicKeyUrl ?? defaults.publicKeyUrl, init(opts, { method: 'GET' }))
  if (!res.ok) throw new WebPushError('server', `public key request answered ${res.status}`, res.status)
  const body = (await res.json()) as { publicKey?: string }
  if (!body.publicKey) throw new WebPushError('server', 'the server has no VAPID public key (push is not configured)')
  return body.publicKey
}

function sameKey(sub: PushSubscription, key: Uint8Array): boolean {
  const current = sub.options?.applicationServerKey
  if (!current) return true // the browser does not tell; keep it
  const a = new Uint8Array(current)
  return a.length === key.length && a.every((b, i) => b === key[i])
}

/**
 * Asks permission if needed, subscribes this browser and saves the
 * subscription on the server. Call it from a click or tap. Resolves with the
 * subscription; rejects with a WebPushError whose code says why not.
 *
 * Calling it again is safe: an existing subscription is reused (and sent to
 * the server again), unless it was made with another VAPID key.
 */
export async function subscribe(opts: PushOptions = {}): Promise<PushSubscription> {
  const reason = unsupportedReason()
  if (reason) throw reason
  const perm = await requestPermission()
  if (perm === 'denied') throw new WebPushError('permission-denied', 'notifications are blocked for this site')
  if (perm !== 'granted') throw new WebPushError('permission-dismissed', 'the permission prompt was closed without a choice')

  const reg = await getRegistration(opts, true)
  if (!reg) throw new WebPushError('no-service-worker', 'no active service worker; register one or pass serviceWorkerUrl')

  const key = urlBase64ToUint8Array(await fetchPublicKey(opts))
  let sub = await reg.pushManager.getSubscription()
  if (sub && !sameKey(sub, key)) {
    await sub.unsubscribe()
    sub = null
  }
  if (!sub) {
    try {
      sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key })
    } catch (err) {
      throw new WebPushError('subscribe-failed', `the push service refused: ${(err as Error).message ?? err}`)
    }
  }

  const json = sub.toJSON()
  const res = await fetch(
    opts.subscriptionsUrl ?? defaults.subscriptionsUrl,
    init(opts, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ endpoint: json.endpoint, keys: json.keys, lang: opts.lang ?? navigator.language }),
    }),
  )
  if (!res.ok) throw new WebPushError('server', `saving the subscription answered ${res.status}`, res.status)
  return sub
}

/**
 * Removes this browser's subscription, on the server and in the browser.
 * Resolves false when there was none.
 */
export async function unsubscribe(opts: PushOptions = {}): Promise<boolean> {
  const sub = await currentSubscription(opts)
  if (!sub) return false
  const res = await fetch(
    opts.subscriptionsUrl ?? defaults.subscriptionsUrl,
    init(opts, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ endpoint: sub.endpoint }),
    }),
  )
  // Unsubscribe in the browser even when the server failed: the server forgets
  // the endpoint on its own at the next push (the push service answers 410).
  await sub.unsubscribe()
  if (!res.ok) throw new WebPushError('server', `removing the subscription answered ${res.status}`, res.status)
  return true
}
