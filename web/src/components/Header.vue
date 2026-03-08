<script setup>
defineProps({
  floorName: { type: String, default: 'OFC' },
  description: { type: String, default: '' },
  agents: { type: Array, default: () => [] },
  hasFurniture: { type: Boolean, default: false },
  sidebarOpen: { type: Boolean, default: false },
})

defineEmits(['toggle-sidebar'])

const agentColors = [
  'bg-green-500/20 text-green-400',
  'bg-purple-500/20 text-purple-400',
  'bg-yellow-500/20 text-yellow-400',
  'bg-blue-500/20 text-blue-400',
  'bg-red-500/20 text-red-400',
]

const borderColors = [
  'border-green-500/40',
  'border-purple-500/40',
  'border-yellow-500/40',
  'border-blue-500/40',
  'border-red-500/40',
]

function colorForAgent(index) {
  return agentColors[index % agentColors.length]
}

function borderForAgent(index) {
  return borderColors[index % borderColors.length]
}
</script>

<template>
  <header class="border-b border-slate-700 px-4 py-3 flex-shrink-0">
    <div class="flex items-center justify-between">
      <div class="flex items-center gap-3">
        <!-- Sidebar toggle (mobile only) -->
        <button
          v-if="hasFurniture"
          class="md:hidden text-slate-400 hover:text-slate-200 transition-colors -ml-1 p-1"
          @click="$emit('toggle-sidebar')"
          :title="sidebarOpen ? 'Close sidebar' : 'Open sidebar'"
        >
          <svg v-if="!sidebarOpen" xmlns="http://www.w3.org/2000/svg" class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M4 6h16M4 12h16M4 18h16" />
          </svg>
          <svg v-else xmlns="http://www.w3.org/2000/svg" class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
        <span class="text-xl font-bold tracking-tight text-slate-100">ofc<span class="ml-1">🎤</span></span>
        <span class="text-slate-600">|</span>
        <div>
          <h1 class="text-sm font-semibold text-slate-300">{{ floorName }}</h1>
          <p v-if="description" class="text-xs text-slate-500">{{ description }}</p>
        </div>
      </div>
      <div class="flex gap-2">
        <div v-for="(agent, i) in agents" :key="agent.id" class="relative group">
          <span
            class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium cursor-default"
            :class="colorForAgent(i)"
          >
            {{ agent.id }}
            <span class="ml-1 opacity-60 hidden sm:inline">{{ agent.type }}</span>
          </span>

          <!-- Tooltip -->
          <div
            class="absolute right-0 top-full mt-2 w-64 rounded-lg border bg-slate-900 shadow-xl text-xs z-50 opacity-0 invisible group-hover:opacity-100 group-hover:visible transition-all duration-150"
            :class="borderForAgent(i)"
          >
            <div class="px-3 py-2 border-b border-slate-800">
              <div class="font-semibold text-slate-200">{{ agent.name || agent.id }}</div>
              <div v-if="agent.prompt_summary" class="text-slate-500 mt-0.5 italic truncate">{{ agent.prompt_summary }}</div>
            </div>
            <div class="px-3 py-2 space-y-1 text-slate-400">
              <div class="flex justify-between">
                <span class="text-slate-500">Type</span>
                <span class="text-slate-300">{{ agent.type }}</span>
              </div>
              <div class="flex justify-between">
                <span class="text-slate-500">Activation</span>
                <span class="text-slate-300">{{ agent.activation }}</span>
              </div>
              <div v-if="agent.model" class="flex justify-between">
                <span class="text-slate-500">Model</span>
                <span class="text-slate-300 truncate ml-2">{{ agent.model }}</span>
              </div>
              <div v-if="agent.command" class="flex justify-between">
                <span class="text-slate-500">Command</span>
                <span class="text-slate-300 font-mono truncate ml-2">{{ agent.command }}</span>
              </div>
              <div v-if="agent.furniture && agent.furniture.length > 0" class="flex justify-between">
                <span class="text-slate-500">Furniture</span>
                <span class="text-slate-300">{{ agent.furniture.join(', ') }}</span>
              </div>
              <div v-if="agent.sandbox" class="flex justify-between">
                <span class="text-slate-500">Sandbox</span>
                <span class="text-slate-300">enabled</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </header>
</template>
