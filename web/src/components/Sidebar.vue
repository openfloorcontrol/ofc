<script setup>
import TaskBoardPanel from './TaskBoardPanel.vue'
import FileListPanel from './FileListPanel.vue'

defineProps({
  furniture: { type: Array, default: () => [] },
  callTool: { type: Function, required: true },
  refreshKey: { type: Number, default: 0 },
  open: { type: Boolean, default: false },
})

defineEmits(['close'])

function isTaskBoard(item) {
  const toolNames = item.tools.map((t) => t.name)
  return toolNames.includes('list_tasks') && toolNames.includes('add_task')
}

function isFileSystem(item) {
  const toolNames = item.tools.map((t) => t.name)
  return toolNames.includes('list_directory')
}
</script>

<template>
  <!-- Mobile: slide-over panel -->
  <aside
    class="
      fixed inset-y-0 left-0 z-30 w-72 border-r border-slate-700 overflow-y-auto bg-slate-900
      transform transition-transform duration-200 ease-in-out
      md:relative md:z-auto md:w-80 md:translate-x-0 md:transition-none
    "
    :class="open ? 'translate-x-0' : '-translate-x-full'"
  >
    <!-- Mobile close button -->
    <div class="flex items-center justify-between px-3 pt-3 md:hidden">
      <span class="text-xs font-semibold text-slate-500 uppercase tracking-wide">Furniture</span>
      <button
        class="text-slate-400 hover:text-slate-200 p-1"
        @click="$emit('close')"
      >
        <svg xmlns="http://www.w3.org/2000/svg" class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </div>
    <div v-for="item in furniture" :key="item.name">
      <TaskBoardPanel
        v-if="isTaskBoard(item)"
        :furnitureName="item.name"
        :callTool="callTool"
        :refreshKey="refreshKey"
      />
      <FileListPanel
        v-else-if="isFileSystem(item)"
        :furnitureName="item.name"
        :callTool="callTool"
        :refreshKey="refreshKey"
      />
      <div v-else class="p-3">
        <h3 class="text-sm font-semibold text-slate-300 uppercase tracking-wide">
          {{ item.name }}
        </h3>
        <p class="text-xs text-slate-500 mt-1">
          {{ item.tools.length }} tool{{ item.tools.length !== 1 ? 's' : '' }}
        </p>
      </div>
    </div>
  </aside>
</template>
