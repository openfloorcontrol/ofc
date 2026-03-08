import { Marked } from 'marked'
import DOMPurify from 'dompurify'
import { getToken } from './useAuth.js'

let tokenCache = null

async function ensureToken() {
  if (!tokenCache) {
    tokenCache = await getToken()
  }
  return tokenCache
}

// Eagerly fetch token so it's ready for synchronous use
ensureToken()

/**
 * Shared markdown renderer with image URL rewriting.
 *
 * Rewrites image src URLs in markdown to /api/v1/file/<path>:
 * - Absolute URLs (http/https/data:) → pass through
 * - Everything else → /api/v1/file/<href>?token=...
 *
 * The backend handles path resolution:
 * - ":furniture/path" → serve via furniture MCP
 * - "path"            → serve from workspace
 */
export function useMarkdown() {
  function rewriteImageUrl(href) {
    if (!href) return href

    // Absolute URLs: pass through
    if (href.startsWith('http://') || href.startsWith('https://') || href.startsWith('data:')) {
      return href
    }

    const token = tokenCache || ''
    const tokenParam = token ? `?token=${encodeURIComponent(token)}` : ''
    return `/api/v1/file/${href}${tokenParam}`
  }

  const md = new Marked({
    breaks: true,
    renderer: {
      image({ href, title, text }) {
        const src = rewriteImageUrl(href)
        const alt = text || ''
        const titleAttr = title ? ` title="${title}"` : ''
        return `<img src="${src}" alt="${alt}"${titleAttr} style="max-width:100%;border-radius:0.5rem;" />`
      },
    },
  })

  function renderMarkdown(text) {
    if (!text) return ''
    const raw = md.parse(text)
    return DOMPurify.sanitize(raw)
  }

  return { renderMarkdown, rewriteImageUrl }
}
