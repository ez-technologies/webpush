// Turns client/dist/sw.js (an ES module) into client/dist/push-sw.js, a
// classic service worker script that installs the handlers itself. Use it as
// your service worker, or importScripts() it from the one you have.
//
// Options come from self.WEBPUSH_OPTIONS (set it before importScripts) and
// from the worker's URL query: ?title=&icon=&badge=&url=&skipWhenFocused=1.
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const dist = join(dirname(fileURLToPath(import.meta.url)), '..', 'client', 'dist')
const module = readFileSync(join(dist, 'sw.js'), 'utf8')
const body = module
  .replace(/^export \{\};?\s*$/gm, '')
  .replace(/^export (async function|function|const) /gm, '$1 ')
  .replace(/^\/\/# sourceMappingURL=.*$/gm, '')
if (/^\s*(export|import)\b/m.test(body)) {
  throw new Error('build-sw: sw.ts must not import or re-export; found a module statement')
}

const script = `// webpush service worker (classic script). Generated from client/src/sw.ts.
(function () {
${body.trim()}

  var q = new URL(self.location.href).searchParams
  var fromQuery = {}
  if (q.get('title')) fromQuery.defaultTitle = q.get('title')
  if (q.get('icon')) fromQuery.icon = q.get('icon')
  if (q.get('badge')) fromQuery.badge = q.get('badge')
  if (q.get('url')) fromQuery.defaultUrl = q.get('url')
  if (q.get('skipWhenFocused') === '1') fromQuery.skipWhenFocused = true
  if (q.get('click')) fromQuery.clickBehavior = q.get('click')
  installPushHandlers(self, Object.assign(fromQuery, self.WEBPUSH_OPTIONS || {}))
})()
`
writeFileSync(join(dist, 'push-sw.js'), script)
console.log('build-sw: wrote client/dist/push-sw.js')
