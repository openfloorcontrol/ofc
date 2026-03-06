let cachedToken = null

/**
 * Get the auth token.
 * Production: injected into HTML by Go server as window.__OFC_TOKEN.
 * Dev mode: fetched from localhost-only /api/v1/auth/token endpoint.
 */
export async function getToken() {
  if (cachedToken) return cachedToken

  // Production: token injected into HTML by Go
  if (window.__OFC_TOKEN) {
    cachedToken = window.__OFC_TOKEN
    return cachedToken
  }

  // Dev mode: fetch from localhost-only endpoint
  try {
    const resp = await fetch('/api/v1/auth/token')
    if (resp.ok) {
      const data = await resp.json()
      cachedToken = data.token
      return cachedToken
    }
  } catch {
    // no token available
  }

  return ''
}

/**
 * Authenticated fetch wrapper. Adds Authorization: Bearer header.
 */
export async function apiFetch(url, options = {}) {
  const token = await getToken()
  if (token) {
    options.headers = {
      ...options.headers,
      Authorization: `Bearer ${token}`,
    }
  }
  return fetch(url, options)
}
