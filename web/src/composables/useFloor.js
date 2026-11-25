import { apiFetch } from './useAuth.js'

// API base of the floor this tab shows, e.g. /api/v1/floors/<id>. Set by
// setFloor before anything else calls the floor's API.
let base = ''

// floorApi returns the URL of path on this tab's floor.
export function floorApi(path) {
  return base + path
}

// setFloor makes id this tab's floor.
export function setFloor(id) {
  base = `/api/v1/floors/${encodeURIComponent(id)}`
}

// listFloors returns the server's floors and whether it manages them
// (ofc building: floors can be added, stopped and deleted).
export async function listFloors() {
  const resp = await apiFetch('/api/v1/floors')
  if (resp.status === 401) {
    throw new Error('unauthorized')
  }
  const body = await resp.json()
  return { manageable: !!body.manageable, floors: body.floors || [] }
}

// apiError turns a failed response into an Error with the server's message.
export async function apiError(resp) {
  const body = await resp.json().catch(() => ({}))
  return new Error(body.error || `${resp.status} ${resp.statusText}`)
}
