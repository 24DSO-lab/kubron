// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from 'vitepress'

const repo = 'https://github.com/24DSO-labs/kubron'
const releases = `${repo}/releases`

export default defineConfig({
  title: 'Kubron',
  description: 'A private Kubernetes Operations Workspace for desktop and web. Multi-cluster operations, Helm and GitOps in one place.',
  lang: 'en-US',
  base: process.env.DOCS_BASE || '/',
  appearance: 'dark',
  head: [
    ['link', { rel: 'icon', href: '/favicon.svg', type: 'image/svg+xml' }],
    ['meta', { name: 'theme-color', content: '#0B0F19' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'Kubron' }],
    ['meta', { property: 'og:title', content: 'Kubron — Kubernetes, under control.' }],
    ['meta', { property: 'og:description', content: 'A private Kubernetes Operations Workspace for desktop and web. Clusters, workloads, Helm and GitOps in one place.' }],
    ['meta', { property: 'og:image', content: 'https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/social-preview.png' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }]
  ],
  locales: {
    root: {
      label: 'English',
      lang: 'en',
      themeConfig: {
        nav: [
          { text: 'Guide', link: '/guide/getting-started' },
          { text: 'Features', link: '/features/' },
          { text: 'Server', link: '/server/' },
          { text: 'Development', link: '/development/' },
          { text: 'Download', link: releases }
        ],
        sidebar: {
          '/guide/': [{ text: 'Guide', items: [
            { text: 'Getting started', link: '/guide/getting-started' },
            { text: 'Installation', link: '/guide/installation' },
            { text: 'First cluster', link: '/guide/first-cluster' },
            { text: 'Keyboard shortcuts', link: '/guide/shortcuts' }
          ]}],
          '/features/': [{ text: 'Features', items: [
            { text: 'Overview', link: '/features/' },
            { text: 'Multi-cluster', link: '/features/multi-cluster' },
            { text: 'Dashboard', link: '/features/dashboard' },
            { text: 'Resources & YAML', link: '/features/resources' },
            { text: 'Helm', link: '/features/helm' },
            { text: 'GitOps', link: '/features/gitops' },
            { text: 'Security & access', link: '/features/security' }
          ]}],
          '/server/': [{ text: 'Server mode', items: [
            { text: 'Overview', link: '/server/' },
            { text: 'Quick start', link: '/server/quick-start' },
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
          { text: 'Руководство', link: '/ru/guide/getting-started' },
          { text: 'Возможности', link: '/ru/features/' },
          { text: 'Сервер', link: '/ru/server/' },
          { text: 'Разработка', link: '/ru/development/' },
          { text: 'Скачать', link: releases }
        ],
        sidebar: {
          '/ru/guide/': [{ text: 'Руководство', items: [
            { text: 'Быстрый старт', link: '/ru/guide/getting-started' },
            { text: 'Установка', link: '/ru/guide/installation' },
            { text: 'Первый кластер', link: '/ru/guide/first-cluster' },
            { text: 'Горячие клавиши', link: '/ru/guide/shortcuts' }
          ]}],
          '/ru/features/': [{ text: 'Возможности', items: [
            { text: 'Обзор', link: '/ru/features/' },
            { text: 'Несколько кластеров', link: '/ru/features/multi-cluster' },
            { text: 'Дашборд', link: '/ru/features/dashboard' },
            { text: 'Ресурсы и YAML', link: '/ru/features/resources' },
            { text: 'Helm', link: '/ru/features/helm' },
            { text: 'GitOps', link: '/ru/features/gitops' },
            { text: 'Безопасность и доступ', link: '/ru/features/security' }
          ]}],
          '/ru/server/': [{ text: 'Серверный режим', items: [
            { text: 'Обзор', link: '/ru/server/' },
            { text: 'Быстрый запуск', link: '/ru/server/quick-start' },
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
