<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'

const props = defineProps({
  furnitureName: { type: String, required: true },
  callTool: { type: Function, required: true },
  refreshKey: { type: Number, default: 0 },
})

const entries = ref([])
const loading = ref(false)
const initialFiles = ref(null) // Set<string>, null until first load completes
const rootPath = ref(null) // discovered from list_allowed_directories

function parseListDirectory(text) {
  if (!text || typeof text !== 'string') return []
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.startsWith('['))
    .map((line) => {
      const dirMatch = line.match(/^\[DIR\]\s+(.+)$/)
      if (dirMatch) return { name: dirMatch[1], type: 'dir' }
      const fileMatch = line.match(/^\[FILE\]\s+(.+)$/)
      if (fileMatch) return { name: fileMatch[1], type: 'file' }
      return null
    })
    .filter(Boolean)
    .sort((a, b) => {
      if (a.type !== b.type) return a.type === 'dir' ? -1 : 1
      return a.name.localeCompare(b.name)
    })
}

async function discoverRootPath() {
  try {
    const result = await props.callTool(props.furnitureName, 'list_allowed_directories')
    // Response: "Allowed directories:\n/path/to/workspace"
    const lines = (result || '').split('\n').map((l) => l.trim()).filter(Boolean)
    // Take the first path (skip "Allowed directories:" header)
    for (const line of lines) {
      if (line.startsWith('/')) {
        rootPath.value = line
        return
      }
    }
  } catch {
    // fallback — try "." anyway
  }
}

async function fetchFiles() {
  loading.value = true
  try {
    const path = rootPath.value || '.'
    const result = await props.callTool(props.furnitureName, 'list_directory', { path })
    entries.value = parseListDirectory(result)

    // Capture initial file set on first successful load
    if (initialFiles.value === null) {
      initialFiles.value = new Set(entries.value.map((e) => e.name))
    }
  } catch {
    entries.value = []
  } finally {
    loading.value = false
  }
}

function isNew(entry) {
  return initialFiles.value !== null && !initialFiles.value.has(entry.name)
}

let debounceTimer = null
function debouncedFetch() {
  clearTimeout(debounceTimer)
  debounceTimer = setTimeout(fetchFiles, 300)
}
onUnmounted(() => clearTimeout(debounceTimer))

watch(() => props.refreshKey, debouncedFetch)
onMounted(async () => {
  await discoverRootPath()
  await fetchFiles()
})
</script>

<template>
  <div class="p-3">
    <div class="flex items-center justify-between mb-3">
      <h3 class="text-sm font-semibold text-slate-300 uppercase tracking-wide">Files</h3>
      <button
        class="text-xs text-cyan-400 hover:text-cyan-300 transition-colors"
        @click="fetchFiles"
        title="Refresh"
      >
        &#x21bb;
      </button>
    </div>

    <div v-if="loading && entries.length === 0" class="text-xs text-slate-500 py-2">
      Loading...
    </div>

    <div v-if="entries.length === 0 && !loading" class="text-[10px] text-slate-600 py-1 italic">
      No files
    </div>

    <div class="space-y-0.5">
      <div
        v-for="entry in entries"
        :key="entry.name"
        class="flex items-center gap-1.5 py-0.5"
      >
        <span class="text-xs flex-shrink-0" :class="entry.type === 'dir' ? 'text-yellow-500' : 'text-slate-500'">
          {{ entry.type === 'dir' ? '\uD83D\uDCC1' : '\uD83D\uDCC4' }}
        </span>
        <span
          class="text-xs truncate"
          :class="entry.type === 'dir' ? 'text-slate-300' : 'text-slate-400'"
        >
          {{ entry.name }}{{ entry.type === 'dir' ? '/' : '' }}
        </span>
        <span
          v-if="isNew(entry)"
          class="flex-shrink-0 w-1.5 h-1.5 rounded-full bg-cyan-400 ml-auto"
          title="New file"
        />
      </div>
    </div>
  </div>
</template>
