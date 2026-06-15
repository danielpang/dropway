// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package servehttp

// The platform HTML pages below are copied VERBATIM from
// edge/serving-worker/src/index.ts (DEFAULT_404_HTML, LINK_EXPIRED_HTML,
// TOO_MANY_REQUESTS_HTML, ACCOUNT_SUSPENDED_HTML, ACCOUNT_OVER_LIMIT_HTML) so the
// self-host server is visually identical to the Worker.

// Default404HTML is the platform default 404 page.
const Default404HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>404 — Not Found</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; }
  h1 { font-size: 3rem; margin: 0 0 .25rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>404</h1>
    <p>This page could not be found.</p>
  </main>
</body>
</html>
`

// LinkExpiredHTML is the platform "link expired" page (410).
const LinkExpiredHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Link expired</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 2rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>This link has expired</h1>
    <p>The share link for this site is no longer active. Ask the site owner for a new one.</p>
  </main>
</body>
</html>
`

// TooManyRequestsHTML is the platform "Too Many Requests" page (429).
const TooManyRequestsHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Too many requests</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 2rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>Too many requests</h1>
    <p>You have made too many requests in a short time. Please wait a moment and try again.</p>
  </main>
</body>
</html>
`

// AccountSuspendedHTML is the platform "account suspended" page (503).
const AccountSuspendedHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Site unavailable</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 2rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>This site is temporarily unavailable</h1>
    <p>The account for this site has been suspended. If you own this site, sign in to your dashboard to resolve it.</p>
  </main>
</body>
</html>
`

// AccountOverLimitHTML is the platform "over limit" page (503).
const AccountOverLimitHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Site unavailable</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { text-align: center; padding: 2rem; max-width: 32rem; }
  h1 { font-size: 2rem; margin: 0 0 .5rem; }
  p { opacity: .7; }
</style>
</head>
<body>
  <main>
    <h1>This site is temporarily unavailable</h1>
    <p>This account has reached its usage limit. If you own this site, sign in to your dashboard to upgrade or wait for the limit to reset.</p>
  </main>
</body>
</html>
`
