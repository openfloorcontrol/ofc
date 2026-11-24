<script setup>
import { ref, computed, onBeforeUnmount } from 'vue'

const props = defineProps({
  disabled: { type: Boolean, default: false },
  // send({ text, attachments }) posts the message; it throws on failure,
  // and the input is kept.
  send: { type: Function, required: true },
  // upload(file) stores an image in the session and returns its
  // attachment ({ id, mime, name, size }); it throws on failure.
  upload: { type: Function, required: true },
})

const input = ref('')
const textarea = ref(null)
const fileInput = ref(null)
const error = ref('')
const dragging = ref(false)
// Staged images: { key, name, preview (object URL), attachment, uploading }
const staged = ref([])
let nextKey = 0

const uploading = computed(() => staged.value.some((s) => s.uploading))
const canSend = computed(
  () => !props.disabled && !uploading.value && (input.value.trim() !== '' || staged.value.length > 0),
)

async function addFiles(files) {
  error.value = ''
  for (const file of files) {
    if (!file.type.startsWith('image/')) {
      error.value = `${file.name}: not an image`
      continue
    }
    const item = { key: nextKey++, name: file.name, preview: URL.createObjectURL(file), attachment: null, uploading: true }
    staged.value.push(item)
    try {
      const attachment = await props.upload(file)
      const current = staged.value.find((s) => s.key === item.key)
      if (current) {
        current.attachment = attachment
        current.uploading = false
      }
    } catch (err) {
      error.value = err.message
      remove(item.key)
    }
  }
}

function remove(key) {
  const i = staged.value.findIndex((s) => s.key === key)
  if (i >= 0) {
    URL.revokeObjectURL(staged.value[i].preview)
    staged.value.splice(i, 1)
  }
}

function clearStaged() {
  for (const s of staged.value) URL.revokeObjectURL(s.preview)
  staged.value = []
}

async function send() {
  if (!canSend.value) return
  const text = input.value.trim()
  if (text.startsWith('/') && staged.value.length > 0) {
    error.value = 'A command takes no images'
    return
  }
  error.value = ''
  try {
    await props.send({ text, attachments: staged.value.map((s) => s.attachment) })
  } catch (err) {
    error.value = err.message
    return
  }
  input.value = ''
  clearStaged()
  if (textarea.value) {
    textarea.value.style.height = 'auto'
  }
}

function onKeydown(e) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

function onPaste(e) {
  const files = [...(e.clipboardData?.files || [])].filter((f) => f.type.startsWith('image/'))
  if (files.length > 0) {
    e.preventDefault()
    addFiles(files)
  }
}

function onDrop(e) {
  dragging.value = false
  const files = [...(e.dataTransfer?.files || [])]
  if (files.length > 0) {
    addFiles(files)
  }
}

function onPick(e) {
  addFiles([...e.target.files])
  e.target.value = ''
}

function autoResize(e) {
  const el = e.target
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 200) + 'px'
}

onBeforeUnmount(clearStaged)
</script>

<template>
  <div
    class="border-t border-slate-700 px-4 py-3 flex-shrink-0"
    :class="dragging ? 'bg-slate-800/60' : ''"
    @dragover.prevent="dragging = true"
    @dragleave="dragging = false"
    @drop.prevent="onDrop"
  >
    <div class="max-w-4xl mx-auto">
      <div v-if="staged.length > 0" class="flex flex-wrap gap-2 mb-2">
        <div
          v-for="s in staged"
          :key="s.key"
          class="relative w-16 h-16 rounded border border-slate-600 overflow-hidden bg-slate-800"
          :title="s.name"
        >
          <img :src="s.preview" :alt="s.name" class="w-full h-full object-cover" :class="s.uploading ? 'opacity-40' : ''" />
          <span v-if="s.uploading" class="absolute inset-0 flex items-center justify-center text-xs text-slate-300">…</span>
          <button
            class="absolute top-0 right-0 w-5 h-5 text-xs leading-5 text-center bg-slate-900/80 text-slate-300 hover:text-white"
            aria-label="Remove image"
            @click="remove(s.key)"
          >×</button>
        </div>
      </div>
      <p v-if="error" class="text-xs text-red-400 mb-2">{{ error }}</p>
      <div class="flex gap-2 items-end">
        <input ref="fileInput" type="file" accept="image/png,image/jpeg,image/gif,image/webp" multiple class="hidden" @change="onPick" />
        <button
          :disabled="disabled"
          class="px-3 py-2 text-slate-400 border border-slate-600 rounded-lg text-sm hover:text-slate-100 hover:border-slate-400 disabled:opacity-40"
          title="Attach images (or paste / drop them)"
          aria-label="Attach images"
          @click="fileInput.click()"
        >📎</button>
        <textarea
          ref="textarea"
          v-model="input"
          :disabled="disabled"
          rows="1"
          placeholder="Type a message... (Enter to send, Shift+Enter for newline, paste or drop images)"
          class="flex-1 bg-slate-800 text-slate-100 border border-slate-600 rounded-lg px-3 py-2 text-sm resize-none focus:outline-none focus:border-cyan-500 placeholder-slate-500"
          @keydown="onKeydown"
          @input="autoResize"
          @paste="onPaste"
        />
        <button
          :disabled="!canSend"
          class="px-4 py-2 bg-cyan-600 text-white rounded-lg text-sm font-medium hover:bg-cyan-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
          @click="send"
        >
          Send
        </button>
      </div>
    </div>
  </div>
</template>
