<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { getToken } from '../composables/useAuth.js'

const props = defineProps({
  furnitureName: { type: String, required: true },
  callTool: { type: Function, required: true },
  refreshKey: { type: Number, default: 0 },
})

const imageExts = new Set(['.png', '.jpg', '.jpeg', '.gif', '.svg', '.webp', '.bmp'])

function isImageFile(name) {
  const dot = name.lastIndexOf('.')
  if (dot < 0) return false
  return imageExts.has(name.substring(dot).toLowerCase())
}

function fileUrl(name) {
  const path = rootPath.value ? rootPath.value + '/' + name : name
  const token = tokenRef.value
  const tokenParam = token ? `?token=${encodeURIComponent(token)}` : ''
  return `/api/v1/file/:${props.furnitureName}/${path}${tokenParam}`
}

const tokenRef = ref('')
getToken().then((t) => { tokenRef.value = t })

const entries = ref([])
const loading = ref(false)
const initialFiles = ref(null) // Set<string>, null until first load completes
const acknowledged = ref(new Set()) // files whose "new" badge has been dismissed
const rootPath = ref(null) // discovered from list_allowed_directories

// File viewer state
const viewerFile = ref(null) // { name, content, error }
const viewerLoading = ref(false)

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
    const lines = (result || '').split('\n').map((l) => l.trim()).filter(Boolean)
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
  return (
    initialFiles.value !== null &&
    !initialFiles.value.has(entry.name) &&
    !acknowledged.value.has(entry.name)
  )
}

function dismissNew(entry) {
  acknowledged.value.add(entry.name)
}

async function openFile(entry) {
  if (entry.type === 'dir') return

  // Images: show directly via file endpoint URL (no need to read content)
  if (isImageFile(entry.name)) {
    viewerFile.value = { name: entry.name, imageUrl: fileUrl(entry.name), content: null, error: null }
    viewerLoading.value = false
    return
  }

  viewerLoading.value = true
  viewerFile.value = { name: entry.name, imageUrl: null, content: null, error: null }
  try {
    const path = rootPath.value ? rootPath.value + '/' + entry.name : entry.name
    let result
    try {
      result = await props.callTool(props.furnitureName, 'read_file', { path })
    } catch {
      // Fallback for servers that use read_text_file instead
      result = await props.callTool(props.furnitureName, 'read_text_file', { path })
    }
    viewerFile.value.content = typeof result === 'string' ? result : JSON.stringify(result, null, 2)
  } catch (err) {
    viewerFile.value.error = err.message || 'Failed to read file'
  } finally {
    viewerLoading.value = false
  }
}

function closeViewer() {
  viewerFile.value = null
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
        class="flex items-center gap-1.5 py-0.5 cursor-pointer rounded px-1 -mx-1 hover:bg-slate-800/50 transition-colors"
        @click="dismissNew(entry)"
        @dblclick="openFile(entry)"
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

    <!-- File viewer modal -->
    <Teleport to="body">
      <div
        v-if="viewerFile"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
        @click.self="closeViewer"
        @keydown.escape="closeViewer"
      >
        <div class="bg-slate-900 border border-slate-700 rounded-lg shadow-2xl w-[90vw] max-w-3xl max-h-[80vh] flex flex-col">
          <div class="flex items-center justify-between px-4 py-3 border-b border-slate-700">
            <span class="text-sm font-mono text-slate-200 truncate">{{ viewerFile.name }}</span>
            <button
              class="text-slate-400 hover:text-slate-200 text-lg leading-none px-1"
              @click="closeViewer"
            >&times;</button>
          </div>
          <div class="flex-1 overflow-auto p-4">
            <div v-if="viewerLoading" class="text-xs text-slate-500">Loading...</div>
            <div v-else-if="viewerFile.error" class="text-xs text-red-400">{{ viewerFile.error }}</div>
            <img v-else-if="viewerFile.imageUrl" :src="viewerFile.imageUrl" :alt="viewerFile.name" class="max-w-full rounded" />
            <pre v-else class="text-xs text-slate-300 font-mono whitespace-pre-wrap leading-relaxed">{{ viewerFile.content }}</pre>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
