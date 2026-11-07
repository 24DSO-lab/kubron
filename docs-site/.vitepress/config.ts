// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from 'vitepress'

const repo = 'https://github.com/24DSO-labs/kubron'
const releases = `${repo}/releases`
const base = process.env.DOCS_BASE || '/'

export default defineConfig({
  title: 'Kubron',
  description: 'A Kubernetes Operations Workspace for desktop and self-hosted web. Multi-cluster operations, Helm and GitOps in one place.',

  lang: 'en-US',
  base,
  appearance: 'dark',
  head: [
    ['link', { rel: 'icon', href: `${base}favicon.svg`, type: 'image/svg+xml' }],
    ['meta', { name: 'theme-color', content: '#0B0F19' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'Kubron' }],
    ['meta', { property: 'og:title', content: 'Kubron — Kubernetes, under control.' }],
    ['meta', { property: 'og:description', content: 'A Kubernetes Operations Workspace for desktop and self-hosted web. Clusters, workloads, Helm and GitOps in one place.' }],
    ['meta', { property: 'og:image', content: 'https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/social-preview.png' }],
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
            { text: 'Keyboard shortcuts', link: '/guide/shortcuts' }
          ]}],
          '/features/': [{ text: 'Features', items: [
            { text: 'Overview', link: '/features/' },
            { text: 'Multi-cluster', link: '/features/multi-cluster' },
            { text: 'Cluster authentication', link: '/features/cluster-auth' },
            { text: 'Dashboard', link: '/features/dashboard' },
            { text: 'Resources & YAML', link: '/features/resources' },
            { text: 'Operations', link: '/features/operations' },
            { text: 'Helm', link: '/features/helm' },
            { text: 'GitOps', link: '/features/gitops' },
            { text: 'Teams & shared access', link: '/features/teams' },
            { text: 'Security & access', link: '/features/security' }
          ]}],
          '/server/': [{ text: 'Web version', items: [
            { text: 'Overview', link: '/server/' },
            { text: 'Local quick start', link: '/server/quick-start' },
            { text: 'Docker & Kubernetes', link: '/server/deploy' },
            { text: 'Configuration', link: '/server/configuration' }
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
            { text: 'Горячие клавиши', link: '/ru/guide/shortcuts' }
          ]}],
          '/ru/features/': [{ text: 'Возможности', items: [
            { text: 'Обзор', link: '/ru/features/' },
            { text: 'Несколько кластеров', link: '/ru/features/multi-cluster' },
            { text: 'Подключение кластера', link: '/ru/features/cluster-auth' },
            { text: 'Дашборд', link: '/ru/features/dashboard' },
            { text: 'Ресурсы и YAML', link: '/ru/features/resources' },
            { text: 'Операции', link: '/ru/features/operations' },
            { text: 'Helm', link: '/ru/features/helm' },
            { text: 'GitOps', link: '/ru/features/gitops' },
            { text: 'Команды и совместный доступ', link: '/ru/features/teams' },
            { text: 'Безопасность и доступ', link: '/ru/features/security' }
          ]}],
          '/ru/server/': [{ text: 'Web-версия', items: [
            { text: 'Обзор', link: '/ru/server/' },
            { text: 'Локальный быстрый запуск', link: '/ru/server/quick-start' },
            { text: 'Docker и Kubernetes', link: '/ru/server/deploy' },
            { text: 'Конфигурация', link: '/ru/server/configuration' }
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
    editLink: {
      pattern: `${repo}/edit/master/docs-site/:path`,
      text: 'Edit this page on GitHub'
    },
    lastUpdated: { text: 'Updated at', formatOptions: { dateStyle: 'medium', timeStyle: 'short' } },
    outline: { level: [2, 3] }
  },
  markdown: { lineNumbers: false },
  cleanUrls: true
})
