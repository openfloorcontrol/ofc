<script setup>
import { ref } from 'vue'

defineProps({
  disabled: { type: Boolean, default: false },
})

const emit = defineEmits(['send'])
const input = ref('')
const textarea = ref(null)

function send() {
  const text = input.value.trim()
  if (!text) return
  emit('send', text)
  input.value = ''
  // Reset textarea height
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

function autoResize(e) {
  const el = e.target
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 200) + 'px'
}
</script>

<template>
  <div class="border-t border-slate-700 px-4 py-3 flex-shrink-0">
    <div class="flex gap-2 items-end max-w-4xl mx-auto">
      <textarea
        ref="textarea"
        v-model="input"
        :disabled="disabled"
        rows="1"
        placeholder="Type a message... (Enter to send, Shift+Enter for newline)"
        class="flex-1 bg-slate-800 text-slate-100 border border-slate-600 rounded-lg px-3 py-2 text-sm resize-none focus:outline-none focus:border-cyan-500 placeholder-slate-500"
        @keydown="onKeydown"
        @input="autoResize"
      />
      <button
        :disabled="disabled || !input.trim()"
        class="px-4 py-2 bg-cyan-600 text-white rounded-lg text-sm font-medium hover:bg-cyan-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        @click="send"
      >
        Send
      </button>
    </div>
  </div>
</template>
