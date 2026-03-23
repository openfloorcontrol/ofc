let cachedToken = null

/**
 * Get the auth token from the URL query parameter.
 * The server prints a URL like http://127.0.0.1:8080?token=abc123
 * on the console at startup (Jupyter-style).
 */
export async function getToken() {
  if (cachedToken) return cachedToken

  // Read token from URL query parameter
  const params = new URLSearchParams(window.location.search)
  const token = params.get('token')
  if (token) {
    cachedToken = token
    return cachedToken
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
