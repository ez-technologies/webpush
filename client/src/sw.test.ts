import { describe, expect, it, vi } from 'vitest'
import { handlePush, installPushHandlers, openOrFocus, parsePushData, type ServiceWorkerGlobalLike, type WindowClientLike } from './sw'

function win(url: string, extra: Partial<WindowClientLike> = {}) {
  return {
    url,
    focused: false,
    visibilityState: 'hidden',
    focus: vi.fn(async () => undefined),
    navigate: vi.fn(async () => undefined),
    postMessage: vi.fn(),
    ...extra,
  }
}

function fakeSW(windows: ReturnType<typeof win>[] = []) {
  const listeners: Record<string, (e: unknown) => void> = {}
  const sw = {
    location: { origin: 'https://app.example.com' },
    registration: { showNotification: vi.fn(async () => undefined) },
    clients: {
      matchAll: vi.fn(async () => windows),
      openWindow: vi.fn(async () => undefined),
    },
    addEventListener: (type: string, l: (e: unknown) => void) => {
      listeners[type] = l
    },
  } satisfies ServiceWorkerGlobalLike
  return { sw, listeners }
}

describe('parsePushData', () => {
  it('reads JSON, plain text and nothing', () => {
    expect(parsePushData({ json: () => ({ title: 'Hi' }), text: () => '' })).toEqual({ title: 'Hi' })
    expect(parsePushData({ json: () => { throw new SyntaxError('x') }, text: () => 'plain words' })).toEqual({ body: 'plain words' })
    expect(parsePushData({ json: () => 42, text: () => '42' })).toEqual({ body: '42' })
    expect(parsePushData(null)).toEqual({})
  })
})

describe('handlePush', () => {
  it('shows the notification with title, body, tag and an absolute url', async () => {
    const { sw } = fakeSW()
    await handlePush(sw, { title: 'Design team', body: '3 new messages', url: '/chat/7', tag: 'chat-7', data: { id: 7 } }, { icon: '/icon.png' })
    expect(sw.registration.showNotification).toHaveBeenCalledWith('Design team', {
      body: '3 new messages',
      tag: 'chat-7',
      renotify: true,
      requireInteraction: false,
      icon: '/icon.png',
      badge: undefined,
      data: { id: 7, url: 'https://app.example.com/chat/7' },
    })
  })

  it('uses defaults for a push without fields', async () => {
    const { sw } = fakeSW()
    await handlePush(sw, {}, { defaultTitle: 'My app', defaultUrl: '/home' })
    const [title, opts] = sw.registration.showNotification.mock.calls[0] as unknown as [string, { data: { url: string }; renotify: boolean }]
    expect(title).toBe('My app')
    expect(opts.data.url).toBe('https://app.example.com/home')
    expect(opts.renotify).toBe(false)
  })

  it('tells open windows, and can stay quiet while the app is in front', async () => {
    const front = win('https://app.example.com/', { focused: true, visibilityState: 'visible' })
    const { sw } = fakeSW([front])
    await handlePush(sw, { title: 'Hi' }, { skipWhenFocused: true })
    expect(front.postMessage).toHaveBeenCalledWith({ type: 'webpush:push', payload: { title: 'Hi' } })
    expect(sw.registration.showNotification).not.toHaveBeenCalled()

    await handlePush(sw, { title: 'Alarm', requireInteraction: true }, { skipWhenFocused: true })
    expect(sw.registration.showNotification).toHaveBeenCalledOnce()

    await handlePush(sw, { title: 'Hi' }, { notifyClients: false })
    expect(front.postMessage).toHaveBeenCalledTimes(2)
  })
})

describe('openOrFocus', () => {
  it('focuses the window already at the url', async () => {
    const other = win('https://app.example.com/')
    const there = win('https://app.example.com/chat/7')
    const { sw } = fakeSW([other, there])
    await openOrFocus(sw, '/chat/7')
    expect(there.focus).toHaveBeenCalled()
    expect(other.focus).not.toHaveBeenCalled()
  })

  it('navigates an open window of the app', async () => {
    const w = win('https://app.example.com/')
    const { sw } = fakeSW([win('https://elsewhere.example.org/'), w])
    await openOrFocus(sw, '/chat/7')
    expect(w.focus).toHaveBeenCalled()
    expect(w.navigate).toHaveBeenCalledWith('https://app.example.com/chat/7')
  })

  it('sends a message instead when asked, or when navigation fails', async () => {
    const w = win('https://app.example.com/')
    const { sw } = fakeSW([w])
    await openOrFocus(sw, '/a', { clickBehavior: 'message' })
    expect(w.navigate).not.toHaveBeenCalled()
    expect(w.postMessage).toHaveBeenCalledWith({ type: 'webpush:click', url: 'https://app.example.com/a' })

    const stuck = win('https://app.example.com/', { navigate: vi.fn(async () => { throw new TypeError('not controlled') }) })
    const second = fakeSW([stuck])
    await openOrFocus(second.sw, '/b')
    expect(stuck.postMessage).toHaveBeenCalledWith({ type: 'webpush:click', url: 'https://app.example.com/b' })
  })

  it('opens a new window when none is open', async () => {
    const { sw } = fakeSW()
    await openOrFocus(sw, '/chat/7')
    expect(sw.clients.openWindow).toHaveBeenCalledWith('https://app.example.com/chat/7')
  })
})

describe('installPushHandlers', () => {
  it('handles push and notificationclick events', async () => {
    const { sw, listeners } = fakeSW()
    installPushHandlers(sw, { defaultTitle: 'My app' })

    const waits: Promise<unknown>[] = []
    listeners.push({ data: { json: () => ({ body: 'hello', url: '/x' }), text: () => '' }, waitUntil: (p: Promise<unknown>) => waits.push(p) })
    await Promise.all(waits)
    expect(sw.registration.showNotification).toHaveBeenCalledWith('My app', expect.objectContaining({ body: 'hello' }))

    const close = vi.fn()
    listeners.notificationclick({ notification: { close, data: { url: 'https://app.example.com/x' } }, waitUntil: (p: Promise<unknown>) => waits.push(p) })
    await Promise.all(waits)
    expect(close).toHaveBeenCalled()
    expect(sw.clients.openWindow).toHaveBeenCalledWith('https://app.example.com/x')
  })
})
