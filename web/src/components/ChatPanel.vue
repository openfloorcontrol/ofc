<script setup>
import { ref, watch, nextTick } from 'vue'
import MessageBubble from './MessageBubble.vue'
import StreamingMessage from './StreamingMessage.vue'

const props = defineProps({
  messages: { type: Array, default: () => [] },
  streamingMessage: { type: Object, default: null },
  agents: { type: Array, default: () => [] },
})

const container = ref(null)
let autoScroll = true

function onScroll() {
  if (!container.value) return
  const { scrollTop, scrollHeight, clientHeight } = container.value
  autoScroll = scrollHeight - scrollTop - clientHeight < 80
}

function scrollToBottom() {
  if (autoScroll && container.value) {
    nextTick(() => {
      container.value.scrollTop = container.value.scrollHeight
    })
  }
}

watch(() => props.messages.length, scrollToBottom)
watch(() => props.streamingMessage?.tokens, scrollToBottom)
</script>

<template>
  <div ref="container" class="flex-1 overflow-y-auto px-4 py-4 space-y-3" @scroll="onScroll">
    <div v-if="messages.length === 0 && !streamingMessage" class="flex items-center justify-center h-full">
      <p class="text-slate-500 text-sm">No messages yet. Type below to begin.</p>
    </div>

    <MessageBubble
      v-for="msg in messages"
      :key="msg.id"
      :message="msg"
      :agents="agents"
    />

    <StreamingMessage
      v-if="streamingMessage"
      :message="streamingMessage"
      :agents="agents"
    />
  </div>
</template>
