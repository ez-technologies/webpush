// Package webpush sends Web Push notifications to the browsers and installed
// web apps (PWAs) of your users.
//
// The pieces are small and replaceable:
//
//   - GenerateVAPIDKeys makes the key pair that identifies your server to the
//     push services.
//   - Subscription is one browser's address for pushes; a Store keeps them by
//     user (MemoryStore here, gormstore for PostgreSQL through GORM).
//   - Sender delivers a Message to every subscription of a user and forgets
//     the ones the push service reports as gone.
//   - Throttler sits in front of a Sender: at most one push per user and group
//     per window, with the messages in between merged into one.
//
// The browser side (subscribe, service worker) is the TypeScript package in
// the client directory of this repository.
package webpush
