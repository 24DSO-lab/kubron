// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from 'vitepress'

const repo = 'https://github.com/24DSO-lab/kubron'
const releases = `${repo}/releases`
const base = process.env.DOCS_BASE || '/'
// Public origin of the site, used for canonical links, hreflang and the sitemap
const siteUrl = (process.env.DOCS_SITE_URL || 'https://kubron.pages.dev').replace(/\/+$/, '')

// 'guide/desktop.md' -> '/guide/desktop', 'index.md' -> '/', 'ru/server/index.md' -> '/ru/server/'
function cleanPath(relativePath: string): string {
  const p = relativePath.replace(/\.md$/, '').replace(/(^|\/)index$/, '$1')
  return '/' + p
}
// Every page exists in both languages, so the counterpart path is computed, not looked up
function languagePaths(relativePath: string): { en: string; ru: string } {
  const isRu = relativePath === 'ru' || relativePath.startsWith('ru/')
  const enRel = isRu ? relativePath.replace(/^ru\/?/, '') || 'index.md' : relativePath
  return { en: cleanPath(enRel), ru: cleanPath('ru/' + enRel).replace(/^\/ru\/\/$/, '/ru/') }
}

export default defineConfig({
  title: 'Kubron',
  description: 'A Kubernetes Operations Workspace for desktop and self-hosted web. Multi-cluster operations, Helm and GitOps in one place.',

  lang: 'en-US',
  base,
  // Hosting notes for maintainers are not part of the public site
  srcExclude: ['README.md', 'DOMAIN.md'],
  sitemap: { hostname: siteUrl },
  // canonical and hreflang links tie the English and Russian versions of a page together
  transformHead({ pageData }) {
    const { relativePath } = pageData
    if (relativePath === '404.md') return []
    const { en, ru } = languagePaths(relativePath)
    const isRu = relativePath === 'ru' || relativePath.startsWith('ru/')
    const url = (path: string) => `${siteUrl}${base.replace(/\/$/, '')}${path}`
    return [
      ['link', { rel: 'canonical', href: url(isRu ? ru : en) }],
      ['link', { rel: 'alternate', hreflang: 'en', href: url(en) }],
      ['link', { rel: 'alternate', hreflang: 'ru', href: url(ru) }],
      ['link', { rel: 'alternate', hreflang: 'x-default', href: url(en) }]
    ]
  },
  appearance: 'dark',
  head: [
    ['link', { rel: 'icon', href: `${base}favicon.svg`, type: 'image/svg+xml' }],
    ['meta', { name: 'theme-color', content: '#0B0F19' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'Kubron' }],
    ['meta', { property: 'og:title', content: 'Kubron — Kubernetes, under control.' }],
    ['meta', { property: 'og:description', content: 'A Kubernetes Operations Workspace for desktop and self-hosted web. Clusters, workloads, Helm and GitOps in one place.' }],
    ['meta', { property: 'og:image', content: 'https://raw.githubusercontent.com/24DSO-lab/kubron/master/docs/images/kubron-banner.png' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }]
  ],
  locales: {
    root: {
      label: 'English',
      lang: 'en',
      themeConfig: {
        nav: [
          { text: 'Get started', link: '/guide/getting-started' },
          { text: 'Features', link: '/features/' },
          { text: 'Web version', link: '/server/' },
          { text: 'Development', link: '/development/' },
          { text: 'Sponsors', link: '/sponsors' },
          { text: 'Download', link: releases }
        ],
        sidebar: {
          '/guide/': [{ text: 'Guide', items: [
            { text: 'What is Kubron', link: '/guide/what-is-kubron' },
            { text: 'Choose a version', link: '/guide/getting-started' },
            { text: 'Desktop', link: '/guide/desktop' },
            { text: 'Requirements & verification', link: '/guide/installation' },
            { text: 'Build from source', link: '/guide/build-from-source' },
            { text: 'Web', link: '/guide/web' },
            { text: 'First cluster', link: '/guide/first-cluster' },
            { text: 'Troubleshooting & FAQ', link: '/guide/troubleshooting' },
            { text: 'Keyboard shortcuts', link: '/guide/shortcuts' }
          ]}],
          '/features/': [{ text: 'Features', items: [
            { text: 'Overview', link: '/features/' },
            { text: 'Multi-cluster', link: '/features/multi-cluster' },
            { text: 'Cluster authentication', link: '/features/cluster-auth' },
            { text: 'Dashboard', link: '/features/dashboard' },
            { text: 'Resources & YAML', link: '/features/resources' },
            { text: 'Working with resources', link: '/features/working-with-resources' },
            { text: 'Operations', link: '/features/operations' },
            { text: 'Debug containers', link: '/features/debug-containers' },
            { text: 'Configuration baseline', link: '/features/baseline' },
            { text: 'Metrics stack & charts', link: '/features/metrics' },
            { text: 'Incidents', link: '/features/intelligence' },
            { text: 'Helm', link: '/features/helm' },
            { text: 'GitOps', link: '/features/gitops' },
            { text: 'Upgrade readiness', link: '/features/upgrade-readiness' },
            { text: 'Teams & shared access', link: '/features/teams' },
            { text: 'Security & access', link: '/features/security' },
            { text: 'Users & audit log', link: '/features/administration' },
            { text: 'Settings', link: '/features/settings' },
            { text: 'Version & updates', link: '/features/updates' },
            { text: 'Report a problem', link: '/features/bug-reports' }
          ]}],
          '/server/': [{ text: 'Web version', items: [
            { text: 'Overview', link: '/server/' },
            { text: 'Local quick start', link: '/server/quick-start' },
            { text: 'Docker & Kubernetes', link: '/server/deploy' },
            { text: 'Configuration', link: '/server/configuration' },
            { text: 'Backup & key rotation', link: '/server/backup' },
            { text: 'High availability', link: '/server/ha' },
            { text: 'Logging, audit & metrics', link: '/server/observability' },
            { text: 'HTTP security', link: '/server/http-security' },
            { text: 'Command reference', link: '/server/cli' }
          ]}],
          '/development/': [{ text: 'Development', items: [
            { text: 'Overview', link: '/development/' },
            { text: 'Architecture', link: '/development/architecture' },
            { text: 'Extending', link: '/development/extending' },
            { text: 'Contributing', link: '/development/contributing' },
            { text: 'Releasing', link: '/development/releasing' }
          ]}]
        }
      }
    },
    ru: {
      label: 'Русский',
      lang: 'ru',
      link: '/ru/',
      themeConfig: {
        nav: [
          { text: 'Начало работы', link: '/ru/guide/getting-started' },
          { text: 'Возможности', link: '/ru/features/' },
          { text: 'Web-версия', link: '/ru/server/' },
          { text: 'Разработка', link: '/ru/development/' },
          { text: 'Поддержать', link: '/ru/sponsors' },
          { text: 'Скачать', link: releases }
        ],
        sidebar: {
          '/ru/guide/': [{ text: 'Руководство', items: [
            { text: 'Что такое Kubron', link: '/ru/guide/what-is-kubron' },
            { text: 'Выбор версии', link: '/ru/guide/getting-started' },
            { text: 'Desktop', link: '/ru/guide/desktop' },
            { text: 'Требования и проверка', link: '/ru/guide/installation' },
            { text: 'Сборка из исходников', link: '/ru/guide/build-from-source' },
            { text: 'Web', link: '/ru/guide/web' },
            { text: 'Первый кластер', link: '/ru/guide/first-cluster' },
            { text: 'Диагностика и FAQ', link: '/ru/guide/troubleshooting' },
            { text: 'Горячие клавиши', link: '/ru/guide/shortcuts' }
          ]}],
          '/ru/features/': [{ text: 'Возможности', items: [
            { text: 'Обзор', link: '/ru/features/' },
            { text: 'Несколько кластеров', link: '/ru/features/multi-cluster' },
            { text: 'Подключение кластера', link: '/ru/features/cluster-auth' },
            { text: 'Дашборд', link: '/ru/features/dashboard' },
            { text: 'Ресурсы и YAML', link: '/ru/features/resources' },
            { text: 'Работа с ресурсами', link: '/ru/features/working-with-resources' },
            { text: 'Операции', link: '/ru/features/operations' },
            { text: 'Debug-контейнеры', link: '/ru/features/debug-containers' },
            { text: 'Baseline конфигурации', link: '/ru/features/baseline' },
            { text: 'Стек метрик и графики', link: '/ru/features/metrics' },
            { text: 'Инциденты', link: '/ru/features/intelligence' },
            { text: 'Helm', link: '/ru/features/helm' },
            { text: 'GitOps', link: '/ru/features/gitops' },
            { text: 'Готовность к обновлению', link: '/ru/features/upgrade-readiness' },
            { text: 'Команды и совместный доступ', link: '/ru/features/teams' },
            { text: 'Безопасность и доступ', link: '/ru/features/security' },
            { text: 'Пользователи и аудит', link: '/ru/features/administration' },
            { text: 'Настройки', link: '/ru/features/settings' },
            { text: 'Версия и обновления', link: '/ru/features/updates' },
            { text: 'Сообщить о проблеме', link: '/ru/features/bug-reports' }
          ]}],
          '/ru/server/': [{ text: 'Web-версия', items: [
            { text: 'Обзор', link: '/ru/server/' },
            { text: 'Локальный быстрый запуск', link: '/ru/server/quick-start' },
            { text: 'Docker и Kubernetes', link: '/ru/server/deploy' },
            { text: 'Конфигурация', link: '/ru/server/configuration' },
            { text: 'Резервное копирование и ключи', link: '/ru/server/backup' },
            { text: 'Высокая доступность', link: '/ru/server/ha' },
            { text: 'Журналы, аудит и метрики', link: '/ru/server/observability' },
            { text: 'Безопасность HTTP', link: '/ru/server/http-security' },
            { text: 'Справочник команд', link: '/ru/server/cli' }
          ]}],
          '/ru/development/': [{ text: 'Разработка', items: [
            { text: 'Обзор', link: '/ru/development/' },
            { text: 'Архитектура', link: '/ru/development/architecture' },
            { text: 'Расширения', link: '/ru/development/extending' },
            { text: 'Участие в разработке', link: '/ru/development/contributing' },
            { text: 'Релизы', link: '/ru/development/releasing' }
          ]}]
        }
      }
    }
  },
  themeConfig: {
    logo: null,
    siteTitle: 'Kubron',
    socialLinks: [{ icon: 'github', link: repo }],
    footer: {
      message: 'Released under the Apache-2.0 License.',
      copyright: 'Copyright © 2026 The Kubron Authors'
    },
    search: { provider: 'local' },
    outline: { level: [2, 3] }
  },
  markdown: { lineNumbers: false },
  cleanUrls: true
})
