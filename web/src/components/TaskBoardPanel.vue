<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'

const props = defineProps({
  furnitureName: { type: String, required: true },
  callTool: { type: Function, required: true },
  refreshKey: { type: Number, default: 0 },
})

const tasks = ref([])
const loading = ref(false)
const newTaskTitle = ref('')
const showAddForm = ref(false)
const collapsed = ref({ todo: false, in_progress: false, done: false })

const statusOrder = ['todo', 'in_progress', 'done']
const statusLabels = { todo: 'Todo', in_progress: 'In Progress', done: 'Done' }
const statusIcons = { todo: '\u2610', in_progress: '\u25C9', done: '\u2713' }
const nextStatus = { todo: 'in_progress', in_progress: 'done', done: 'todo' }

const grouped = computed(() => {
  const groups = { todo: [], in_progress: [], done: [] }
  for (const task of tasks.value) {
    const status = task.status || 'todo'
    if (groups[status]) {
      groups[status].push(task)
    } else {
      groups.todo.push(task)
    }
  }
  return groups
})

async function fetchTasks() {
  loading.value = true
  try {
    const result = await props.callTool(props.furnitureName, 'list_tasks')
    if (Array.isArray(result)) {
      tasks.value = result
    } else if (result && Array.isArray(result.tasks)) {
      tasks.value = result.tasks
    } else {
      tasks.value = []
    }
  } catch {
    tasks.value = []
  } finally {
    loading.value = false
  }
}

// Debounce refreshes — multiple furniture_updated events can fire in rapid succession
let debounceTimer = null
function debouncedFetchTasks() {
  clearTimeout(debounceTimer)
  debounceTimer = setTimeout(fetchTasks, 300)
}
onUnmounted(() => clearTimeout(debounceTimer))

async function cycleStatus(task) {
  const newStatus = nextStatus[task.status || 'todo']
  try {
    await props.callTool(props.furnitureName, 'update_task', {
      id: task.id,
      status: newStatus,
    })
    task.status = newStatus
  } catch {
    await fetchTasks()
  }
}

async function addTask() {
  const title = newTaskTitle.value.trim()
  if (!title) return
  try {
    await props.callTool(props.furnitureName, 'add_task', { title })
    newTaskTitle.value = ''
    showAddForm.value = false
    // No fetchTasks here — the furniture_updated event will trigger it
  } catch {
    // ignore
  }
}

function toggleGroup(status) {
  collapsed.value[status] = !collapsed.value[status]
}

watch(() => props.refreshKey, debouncedFetchTasks)
onMounted(fetchTasks)
</script>

<template>
  <div class="p-3">
    <div class="flex items-center justify-between mb-3">
      <h3 class="text-sm font-semibold text-slate-300 uppercase tracking-wide">Tasks</h3>
      <button
        class="text-xs text-cyan-400 hover:text-cyan-300 transition-colors"
        @click="fetchTasks"
        title="Refresh"
      >
        &#x21bb;
      </button>
    </div>

    <div v-if="loading && tasks.length === 0" class="text-xs text-slate-500 py-2">
      Loading...
    </div>

    <div v-for="status in statusOrder" :key="status" class="mb-2">
      <button
        class="flex items-center gap-1 w-full text-left text-xs font-medium text-slate-400 hover:text-slate-300 py-1 transition-colors"
        @click="toggleGroup(status)"
      >
        <span class="text-[10px]">{{ collapsed[status] ? '\u25B8' : '\u25BE' }}</span>
        <span>{{ statusLabels[status] }}</span>
        <span class="text-slate-500 ml-1">({{ grouped[status].length }})</span>
      </button>

      <div v-if="!collapsed[status]" class="space-y-1 ml-2">
        <div
          v-for="task in grouped[status]"
          :key="task.id"
          class="flex items-start gap-2 py-1 group"
        >
          <button
            class="text-sm flex-shrink-0 mt-0.5 cursor-pointer hover:opacity-80 transition-opacity"
            :class="{
              'text-slate-500': status === 'todo',
              'text-cyan-400': status === 'in_progress',
              'text-green-400': status === 'done',
            }"
            :title="'Click to change to ' + statusLabels[nextStatus[status]]"
            @click="cycleStatus(task)"
          >
            {{ statusIcons[status] }}
          </button>
          <div class="min-w-0 flex-1">
            <p
              class="text-xs leading-tight"
              :class="{
                'text-slate-300': status !== 'done',
                'text-slate-500 line-through': status === 'done',
              }"
            >
              {{ task.title }}
            </p>
            <p v-if="task.assignee" class="text-[10px] text-slate-500 mt-0.5">
              {{ task.assignee }}
            </p>
          </div>
        </div>
        <div v-if="grouped[status].length === 0" class="text-[10px] text-slate-600 py-1 italic">
          No tasks
        </div>
      </div>
    </div>

    <!-- Add task -->
    <div class="mt-3 border-t border-slate-700 pt-2">
      <div v-if="showAddForm" class="flex gap-1">
        <input
          v-model="newTaskTitle"
          class="flex-1 bg-slate-800 text-slate-200 text-xs border border-slate-600 rounded px-2 py-1 focus:outline-none focus:border-cyan-500"
          placeholder="Task title..."
          @keydown.enter="addTask"
          @keydown.escape="showAddForm = false"
        />
        <button
          class="text-xs px-2 py-1 bg-cyan-600 text-white rounded hover:bg-cyan-500 transition-colors"
          @click="addTask"
        >
          Add
        </button>
      </div>
      <button
        v-else
        class="text-xs text-slate-500 hover:text-slate-400 transition-colors"
        @click="showAddForm = true"
      >
        + Add task
      </button>
    </div>
  </div>
</template>
