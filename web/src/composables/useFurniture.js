import { ref } from 'vue'
import { apiFetch } from './useAuth.js'
import { floorApi } from './useFloor.js'

/**
 * Composable for furniture state: fetches available furniture and proxies tool calls.
 */
export function useFurniture() {
  const furniture = ref([])

  async function fetchFurniture() {
    try {
      const resp = await apiFetch(floorApi('/furniture'))
      const data = await resp.json()
      furniture.value = data.furniture || []
    } catch {
      furniture.value = []
    }
  }

  async function callTool(furnitureName, tool, args = {}) {
    const resp = await apiFetch(floorApi(`/furniture/${furnitureName}/call`), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool, args }),
    })
    const data = await resp.json()
    return data.result
  }

  return { furniture, fetchFurniture, callTool }
}
