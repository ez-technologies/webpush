// Service worker side of Web Push: show the notification a push carries and
// focus or open the app when it is tapped.
//
// Use it from a bundled service worker:
//
//   import { installPushHandlers } from '@ez-technologies/webpush/sw'
//   installPushHandlers(self, { defaultTitle: 'My app', icon: '/icon-192.png' })
//
// or, without a bundler, importScripts('/push-sw.js') (see the README).
//
// This file has no imports on purpose: the build turns it into the classic
// script push-sw.js as well.

/** What the Go sender puts in a push (webpush.Message). */
export interface PushPayload {
  title?: string
  body?: string
  /** Opened or focused on tap. Relative URLs are resolved against the service worker's origin. */
  url?: string
  /** A newer notification with the same tag replaces the older one. */
  tag?: string
  icon?: string
  badge?: string
  requireInteraction?: boolean
  data?: Record<string, unknown>
}

export interface PushHandlerOptions {
  /** Title when a push has none. Default 'New notification'. */
  defaultTitle?: string
  /** Opened on tap when a push has no url. Default '/'. */
  defaultUrl?: string
  /** Default icon and badge image URLs. */
  icon?: string
  badge?: string
  /**
   * Do not show a notification while a window of the app is visible and
   * focused (the page shows it itself, from the 'webpush:push' message).
   * Pushes with requireInteraction are always shown. Default false.
   */
  skipWhenFocused?: boolean
  /** Post {type: 'webpush:push', payload} to the app's windows on every push. Default true. */
  notifyClients?: boolean
  /**
   * On tap, a window already showing the URL is focused. Otherwise, with
   * 'navigate' (default) an open window is focused and navigated to it; with
   * 'message' it is focused and sent {type: 'webpush:click', url} for the app
   * to route itself. With no window open, the URL opens in a new one.
   */
  clickBehavior?: 'navigate' | 'message'
}

// The few service worker APIs used here, typed locally so this file compiles
// with the DOM library and is easy to fake in tests.
export interface WindowClientLike {
  url: string
  focused?: boolean
  visibilityState?: string
  focus(): Promise<unknown>
  navigate?(url: string): Promise<unknown>
  postMessage(message: unknown): void
}

export interface NotificationLike {
  data?: unknown
  close(): void
}

export interface ServiceWorkerGlobalLike {
  location: { origin: string }
  registration: { showNotification(title: string, options?: NotificationOptions & Record<string, unknown>): Promise<void> }
  clients: {
    matchAll(options: { type: 'window'; includeUncontrolled: boolean }): Promise<readonly WindowClientLike[]>
    openWindow(url: string): Promise<unknown>
  }
  addEventListener(type: string, listener: (event: any) => void): void
}

interface PushMessageDataLike {
  json(): unknown
  text(): string
}

/** Reads a push's data: JSON as sent by the Go sender, or plain text as the body. */
export function parsePushData(data: PushMessageDataLike | null | undefined): PushPayload {
  if (!data) return {}
  try {
    const value = data.json()
    if (value && typeof value === 'object') return value as PushPayload
    return { body: String(value) }
  } catch {
    return { body: data.text() }
  }
}

function absolute(sw: ServiceWorkerGlobalLike, url: string): string {
  try {
    return new URL(url, sw.location.origin).href
  } catch {
    return new URL('/', sw.location.origin).href
  }
}

async function windows(sw: ServiceWorkerGlobalLike): Promise<readonly WindowClientLike[]> {
  return sw.clients.matchAll({ type: 'window', includeUncontrolled: true })
}

/** Shows the notification for one push (the 'push' event's work). */
export async function handlePush(sw: ServiceWorkerGlobalLike, payload: PushPayload, opts: PushHandlerOptions = {}): Promise<void> {
  const open = await windows(sw)
  if (opts.notifyClients !== false) {
    for (const w of open) w.postMessage({ type: 'webpush:push', payload })
  }
  const looking = open.some((w) => w.focused && w.visibilityState === 'visible')
  if (opts.skipWhenFocused && looking && !payload.requireInteraction) return

  const url = absolute(sw, payload.url || opts.defaultUrl || '/')
  await sw.registration.showNotification(payload.title || opts.defaultTitle || 'New notification', {
    body: payload.body || '',
    tag: payload.tag || undefined,
    renotify: Boolean(payload.tag),
    requireInteraction: Boolean(payload.requireInteraction),
    icon: payload.icon || opts.icon,
    badge: payload.badge || opts.badge,
    data: { ...(payload.data ?? {}), url },
  })
}

/** Focuses a window of the app, or opens one, at url (the 'notificationclick' event's work). */
export async function openOrFocus(sw: ServiceWorkerGlobalLike, url: string, opts: PushHandlerOptions = {}): Promise<unknown> {
  const target = absolute(sw, url)
  const open = (await windows(sw)).filter((w) => w.url.startsWith(sw.location.origin))
  const exact = open.find((w) => w.url === target)
  if (exact) return exact.focus()
  const any = open[0]
  if (!any) return sw.clients.openWindow(target)
  if ((opts.clickBehavior ?? 'navigate') === 'navigate' && typeof any.navigate === 'function') {
    try {
      await any.focus()
      return await any.navigate(target)
    } catch {
      // A window the worker does not control cannot be navigated; tell it instead.
    }
  }
  any.postMessage({ type: 'webpush:click', url: target })
  return any.focus()
}

/** Adds the 'push' and 'notificationclick' listeners to the service worker. */
export function installPushHandlers(sw: ServiceWorkerGlobalLike, opts: PushHandlerOptions = {}): void {
  sw.addEventListener('push', (event: { data?: PushMessageDataLike | null; waitUntil(p: Promise<unknown>): void }) => {
    event.waitUntil(handlePush(sw, parsePushData(event.data), opts))
  })
  sw.addEventListener('notificationclick', (event: { notification: NotificationLike; waitUntil(p: Promise<unknown>): void }) => {
    event.notification.close()
    const data = (event.notification.data ?? {}) as { url?: string }
    event.waitUntil(openOrFocus(sw, data.url || opts.defaultUrl || '/', opts))
  })
}
