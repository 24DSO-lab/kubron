<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="chart-card">
    <div class="chart-card-header">
      <h3 class="chart-card-title">{{ title }}</h3>
      <div class="chart-range-buttons">
        <button
          v-for="range in timeRanges"
          :key="range.value"
          @click="selectedRange = range.value"
          :class="[
            'chart-range-btn',
            selectedRange === range.value ? 'active' : 'inactive'
          ]"
        >
          {{ $t(`metrics.ranges.${range.key}`) }}
        </button>
      </div>
    </div>

    <div v-if="loading && !hasData" class="chart-loading">
      <div class="chart-spinner"></div>
    </div>

    <div v-else-if="error && !hasData" class="chart-error">
      <p class="chart-error-text">{{ error }}</p>
    </div>

    <div v-else-if="!hasData" class="chart-no-data">
      <p class="chart-no-data-text">{{ $t('metrics.chart.noData') }}</p>
    </div>

    <div v-else class="chart-canvas-wrapper">
      <div v-if="loading" class="chart-canvas-loading">
        <div class="chart-spinner small"></div>
      </div>
      <Line :data="chartData" :options="chartOptions" />
    </div>

    <!-- Legend -->
    <div v-if="hasData && series.length > 1" class="chart-legend">
      <div
        v-for="(s, i) in series"
        :key="s.label"
        class="chart-legend-item"
      >
        <span
          class="chart-legend-dot"
          :style="{ backgroundColor: seriesColors[i] }"
        ></span>
        {{ s.label }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { Line } from 'vue-chartjs'
import { errorText } from '../../composables/errorText'
import { useTheme } from '../../composables/useTheme'
import { chartPalette } from '../../composables/themePalettes'
import { formatClock, formatMonthDay } from '../../i18n/format'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler,
} from 'chart.js'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Title, Tooltip, Legend, Filler)

interface SeriesData {
  label: string
  data: [number, number][]
}

const props = withDefaults(defineProps<{
  title: string
  unit?: string
  fetchData: (startUnix: number, endUnix: number, step: number) => Promise<SeriesData[]>
  formatValue?: (value: number) => string
  maxY?: number
  refreshIntervalMs?: number
}>(), {
  unit: '',
  refreshIntervalMs: 30000,
})

const palette = [
  '#3B82F6', '#10B981', '#F59E0B', '#EF4444', '#8B5CF6',
  '#EC4899', '#06B6D4', '#84CC16', '#F97316', '#6366F1',
]

const timeRanges = [
  { key: 'h1', value: 3600 },
  { key: 'h6', value: 21600 },
  { key: 'h24', value: 86400 },
  { key: 'd7', value: 604800 },
]

const selectedRange = ref(3600)
const series = ref<SeriesData[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
let refreshTimer: ReturnType<typeof setInterval> | null = null

const hasData = computed(() => series.value.some(s => s.data.length > 0))

const seriesColors = computed(() =>
  series.value.map((_, i) => palette[i % palette.length])
)

const chartData = computed(() => {
  if (!hasData.value) {
    return { labels: [], datasets: [] }
  }

  // Объединяем все timestamps и сортируем
  const allTimestamps = new Set<number>()
  series.value.forEach(s => s.data.forEach(([ts]) => allTimestamps.add(ts)))
  const timestamps = Array.from(allTimestamps).sort((a, b) => a - b)

  const labels = timestamps.map(ts => {
    const d = new Date(ts * 1000)
    return selectedRange.value <= 86400 ? formatClock(d) : formatMonthDay(d)
  })

  const datasets = series.value.map((s, i) => {
    const dataMap = new Map(s.data)
    return {
      label: s.label,
      data: timestamps.map(ts => dataMap.get(ts) ?? null),
      borderColor: seriesColors.value[i],
      backgroundColor: 'transparent',
      tension: 0.3,
      pointRadius: 0,
      pointHitRadius: 10,
      borderWidth: 2,
      spanGaps: true,
    }
  })

  return { labels, datasets }
})

const { resolved: appTheme } = useTheme()
const themeColors = computed(() => chartPalette(appTheme.value))

const chartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  animation: { duration: 300 },
  interaction: {
    intersect: false,
    mode: 'index' as const,
  },
  plugins: {
    legend: { display: false },
    tooltip: {
      backgroundColor: themeColors.value.tooltipBackground,
      titleColor: themeColors.value.tooltipTitle,
      bodyColor: themeColors.value.tooltipBody,
      borderColor: themeColors.value.tooltipBorder,
      borderWidth: 1,
      padding: 8,
      callbacks: {
        label: (context: any) => {
          const val = context.parsed.y
          if (val === null) return ''
          const formatted = props.formatValue ? props.formatValue(val) : val.toFixed(2)
          return `${context.dataset.label}: ${formatted} ${props.unit}`.trim()
        },
      },
    },
  },
  scales: {
    x: {
      grid: { color: themeColors.value.grid, drawBorder: false },
      ticks: { color: themeColors.value.tick, maxTicksLimit: 8, font: { size: 10 }, maxRotation: 0 },
    },
    y: {
      min: 0,
      max: props.maxY || undefined,
      grid: { color: themeColors.value.grid, drawBorder: false },
      ticks: {
        color: themeColors.value.tick,
        font: { size: 10 },
        maxTicksLimit: 5,
        callback: (value: number | string) => {
          const num = typeof value === 'string' ? parseFloat(value) : value
          if (props.formatValue) return props.formatValue(num)
          return num.toFixed(2)
        },
      },
    },
  },
}))

async function loadData() {
  try {
    loading.value = true
    error.value = null
    const endUnix = Math.floor(Date.now() / 1000)
    const startUnix = endUnix - selectedRange.value
    const step = Math.max(Math.floor(selectedRange.value / 200), 15)
    series.value = await props.fetchData(startUnix, endUnix, step)
  } catch (e) {
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}

watch(selectedRange, () => loadData())

onMounted(() => {
  loadData()
  refreshTimer = setInterval(loadData, props.refreshIntervalMs)
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>
