<script setup>
import { computed } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import ToolCall from './ToolCall.vue'

const props = defineProps({
  message: { type: Object, required: true },
  agents: { type: Array, default: () => [] },
})

const senderColors = {
  '@user': 'text-cyan-400',
}

const agentColorList = [
  'text-green-400',
  'text-purple-400',
  'text-yellow-400',
  'text-blue-400',
  'text-red-400',
]

const senderColor = computed(() => {
  if (senderColors[props.message.from]) return senderColors[props.message.from]
  const idx = props.agents.findIndex((a) => a.id === props.message.from)
  if (idx >= 0) return agentColorList[idx % agentColorList.length]
  return 'text-slate-400'
})

const isUser = computed(() => props.message.from === '@user')

const renderedContent = computed(() => {
  if (!props.message.content) return ''
  const raw = marked.parse(props.message.content, { breaks: true })
  return DOMPurify.sanitize(raw)
})

// Merge tool calls from streaming and tool interactions from final message
const toolCalls = computed(() => {
  if (props.message.toolCalls && props.message.toolCalls.length > 0) {
    return props.message.toolCalls
  }
  if (props.message.toolInteractions && props.message.toolInteractions.length > 0) {
    return props.message.toolInteractions.map((ti) => ({
      title: ti.command || ti.Command,
      output: ti.output || ti.Output,
      loading: false,
    }))
  }
  return []
})
</script>

<template>
  <div class="group">
    <div class="flex items-baseline gap-2 mb-1">
      <span class="text-sm font-semibold" :class="senderColor">{{ message.from }}</span>
      <span v-if="message.isPass" class="text-xs text-slate-500">[PASS]</span>
      <span v-if="message.isError" class="text-xs text-red-400">[ERROR]</span>
    </div>

    <div v-if="toolCalls.length > 0" class="space-y-1 mb-2">
      <ToolCall v-for="(tc, i) in toolCalls" :key="i" :toolCall="tc" />
    </div>

    <div
      v-if="message.content && !message.isPass"
      class="prose prose-invert prose-sm max-w-none text-slate-200"
      :class="{ 'pl-0': isUser, 'pl-0': !isUser }"
      v-html="renderedContent"
    />
  </div>
</template>
