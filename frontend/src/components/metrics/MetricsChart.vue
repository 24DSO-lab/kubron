<!-- Copyright 2026 The Kubron Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<template>
  <div class="chart-card">
    <div class="chart-card-header">
      <h3 class="chart-card-title">{{ title }}</h3>
      <div class="chart-range-buttons">
        <button v-for="range in timeRanges" :key="range.value" @click="selectedRange = range.value" :class="[
          'chart-range-btn',
          selectedRange === range.value ? 'active' : 'inactive'
        ]">
          {{ $t(`metrics.ranges.${range.key}`) }}
        </button>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading && !dataPoints.length" class="chart-loading">
      <div class="chart-spinner"></div>
    </div>

    <!-- Error -->
    <div v-else-if="error && !dataPoints.length" class="chart-error">
      <div class="chart-error-content">
        <svg class="chart-error-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
            d="M12 9v2m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <p class="chart-error-text">{{ error }}</p>
        <button @click="loadData" class="chart-retry-btn">
          {{ $t('common.retry') }}
        </button>
      </div>
    </div>

    <!-- No data -->
    <div v-else-if="!dataPoints.length" class="chart-no-data">
      <p class="chart-no-data-text">{{ $t('metrics.chart.noData') }}</p>
    </div>

    <!-- Chart -->
    <div v-else class="chart-canvas-wrapper">
      <div v-if="loading" class="chart-canvas-loading">
        <div class="chart-spinner small"></div>
      </div>
      <Line :data="chartData" :options="chartOptions" />
    </div>

    <!-- Current value -->
    <div v-if="currentValue !== null" class="chart-current-value-row">
      <span class="chart-current-label">{{ $t('metrics.chart.current') }}</span>
      <div>
        <span class="chart-current-value">{{ formattedCurrentValue }}</span>
        <span v-if="unit" class="chart-current-unit">{{ unit }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { Line } from 'vue-chartjs'
import { useI18n } from 'vue-i18n'
import { useTheme } from '../../composables/useTheme'
import { chartPalette } from '../../composables/themePalettes'
import { errorText } from '../../composables/errorText'
import { formatClock, formatDateTime, formatMonthDay } from '../../i18n/format'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Filler,
} from 'chart.js'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Title, Tooltip, Filler)

const props = withDefaults(defineProps<{
  title: string
  unit?: string
  color?: string
  fetchData: (startUnix: number, endUnix: number, step: number) => Promise<[number, number][]>
  formatValue?: (value: number) => string
  maxY?: number
  refreshIntervalMs?: number
}>(), {
  unit: '',
  color: '#3B82F6',
  refreshIntervalMs: 30000,
})

const { t } = useI18n()

const timeRanges = [
  { key: 'h1', value: 3600 },
  { key: 'h6', value: 21600 },
  { key: 'h24', value: 86400 },
  { key: 'd7', value: 604800 },
]

const selectedRange = ref(3600)
const dataPoints = ref<[number, number][]>([])
const loading = ref(false)
const error = ref<string | null>(null)
let refreshTimer: ReturnType<typeof setInterval> | null = null

const currentValue = computed(() => {
  if (!dataPoints.value.length) return null
  return dataPoints.value[dataPoints.value.length - 1][1]
})

const formattedCurrentValue = computed(() => {
  if (currentValue.value === null) return t('common.dash')
  if (props.formatValue) return props.formatValue(currentValue.value)
  return currentValue.value.toFixed(2)
})

const chartData = computed(() => {
  const points = dataPoints.value
  return {
    labels: points.map(([ts]) => formatTimestamp(ts)),
    datasets: [
      {
        data: points.map(([, v]) => v),
        borderColor: props.color,
        backgroundColor: hexToRgba(props.color, 0.1),
        fill: true,
        tension: 0.3,
        pointRadius: 0,
        pointHitRadius: 10,
        borderWidth: 2,
      },
    ],
  }
})

const { resolved: appTheme } = useTheme()
const palette = computed(() => chartPalette(appTheme.value))

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
      backgroundColor: palette.value.tooltipBackground,
      titleColor: palette.value.tooltipTitle,
      bodyColor: palette.value.tooltipBody,
      borderColor: palette.value.tooltipBorder,
      borderWidth: 1,
      padding: 8,
      displayColors: false,
      callbacks: {
        title: (items: any[]) => {
          if (!items.length) return ''
          const idx = items[0].dataIndex
          const ts = dataPoints.value[idx]?.[0]
          if (!ts) return ''
          return formatDateTime(ts * 1000)
        },
        label: (context: any) => {
          const val = context.parsed.y
          const formatted = props.formatValue ? props.formatValue(val) : val.toFixed(2)
          return `${formatted} ${props.unit}`.trim()
        },
      },
    },
  },
  scales: {
    x: {
      grid: { color: palette.value.grid, drawBorder: false },
      ticks: {
        color: palette.value.tick,
        maxTicksLimit: 8,
        font: { size: 10 },
        maxRotation: 0,
      },
    },
    y: {
      min: 0,
      max: props.maxY || undefined,
      grid: { color: palette.value.grid, drawBorder: false },
      ticks: {
        color: palette.value.tick,
        font: { size: 10 },
        maxTicksLimit: 5,
        callback: (value: number | string) => {
          const num = typeof value === 'string' ? parseFloat(value) : value
          if (props.formatValue) return props.formatValue(num)
          return num
        },
      },
    },
  },
}))

function formatTimestamp(ts: number): string {
  const d = new Date(ts * 1000)
  return selectedRange.value <= 86400 ? formatClock(d) : formatMonthDay(d)
}

function hexToRgba(hex: string, alpha: number): string {
  const r = parseInt(hex.slice(1, 3), 16)
  const g = parseInt(hex.slice(3, 5), 16)
  const b = parseInt(hex.slice(5, 7), 16)
  return `rgba(${r}, ${g}, ${b}, ${alpha})`
}

async function loadData() {
  try {
    loading.value = true
    error.value = null
    const endUnix = Math.floor(Date.now() / 1000)
    const startUnix = endUnix - selectedRange.value
    // ~200 точек на график
    const step = Math.max(Math.floor(selectedRange.value / 200), 15)
    dataPoints.value = await props.fetchData(startUnix, endUnix, step)
  } catch (e) {
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}

function startRefresh() {
  stopRefresh()
  refreshTimer = setInterval(loadData, props.refreshIntervalMs)
}

function stopRefresh() {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
}

watch(selectedRange, () => {
  loadData()
})

onMounted(() => {
  loadData()
  startRefresh()
})

onUnmounted(() => {
  stopRefresh()
})
</script>
