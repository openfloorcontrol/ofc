<script setup>
import { computed } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import ToolCall from './ToolCall.vue'

const props = defineProps({
  message: { type: Object, required: true },
  agents: { type: Array, default: () => [] },
})

const agentColorList = [
  'text-green-400',
  'text-purple-400',
  'text-yellow-400',
  'text-blue-400',
  'text-red-400',
]

const senderColor = computed(() => {
  const idx = props.agents.findIndex((a) => a.id === props.message.from)
  if (idx >= 0) return agentColorList[idx % agentColorList.length]
  return 'text-slate-400'
})

const renderedContent = computed(() => {
  if (!props.message.tokens) return ''
  const raw = marked.parse(props.message.tokens, { breaks: true })
  return DOMPurify.sanitize(raw)
})
</script>

<template>
  <div class="group">
    <div class="flex items-baseline gap-2 mb-1">
      <span class="text-sm font-semibold" :class="senderColor">{{ message.from }}</span>
      <span class="inline-block w-2 h-4 bg-slate-400 animate-pulse rounded-sm" />
    </div>

    <div v-if="message.toolCalls.length > 0" class="space-y-1 mb-2">
      <ToolCall v-for="(tc, i) in message.toolCalls" :key="i" :toolCall="tc" />
    </div>

    <div
      v-if="message.tokens"
      class="prose prose-invert prose-sm max-w-none text-slate-200"
      v-html="renderedContent"
    />
  </div>
</template>
