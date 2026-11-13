// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineAsyncComponent, type Component } from 'vue'

export interface ViewConfig {
  component: Component
}

// Ленивая загрузка для тяжёлых/редких компонентов
const lazy = (path: string) => defineAsyncComponent(() => import(`../components/views/${path}.vue`))

/*
 * Кастомные view, не вписывающиеся в каркас ресурсов (список + панель деталей):
 *  - overview — дашборд кластера с графиками
 *  - argo-dashboard — сводка по Argo CD Application
 *  - flux-dashboard — сводка по объектам Flux CD
 *  - settings — форма настроек кластера
 *  - baseline — baseline конфигурации и проверка дрейфа
 *  - teams — команды, участники и кластеры по командам (только сервисный режим)
 *  - users — управление пользователями и ролями (только для users.manage)
 *  - audit — журнал аудита (только для audit.read)
 * Все остальные view (k8s-ресурсы, helm, port forwarding) — data-driven, см. resources/registry.ts
 */
export const viewRegistry: Record<string, ViewConfig> = {
  overview: {
    component: lazy('OverviewView'),
  },
  // Дашборд Argo CD (пункт группы Argo CD; видна только если в кластере есть Argo CD)
  'argo-dashboard': {
    component: lazy('ArgoDashboardView'),
  },
  // Дашборд Flux CD (пункт группы Flux CD; видна только если в кластере есть Flux)
  'flux-dashboard': {
    component: lazy('FluxDashboardView'),
  },
  'helm-repos': { component: lazy('HelmReposView') },
'helm-catalog': { component: lazy('HelmCatalogView') },
  'helm-ops': {
    component: lazy('HelmOperationsView'),
  },
  // Baseline конфигурации и дрейф (ADR-0027); бэкенд проверяет права read и baseline.manage
  baseline: { component: lazy('BaselineView') },
  settings: {
    component: lazy('ClusterSettings'),
  },
  // Управление пользователями; бэкенд всё равно требует право users.manage
  users: {
    component: lazy('UsersView'),
  },
  // Команды и их состав (сервисный режим); права проверяет бэкенд
  teams: {
    component: lazy('TeamsView'),
  },
  // Журнал аудита; бэкенд всё равно требует право audit.read
  audit: {
    component: lazy('AuditView'),
  },
}
